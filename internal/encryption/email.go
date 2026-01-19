package encryption

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/mail"
	"strings"

	"go.uber.org/zap"
)

// Header names for Nostr encryption metadata
const (
	HeaderNostrEncrypted = "X-Nostr-Encrypted"
	HeaderNostrSender    = "X-Nostr-Sender"
	HeaderNostrRecipient = "X-Nostr-Recipient"
	HeaderNostrAlgorithm = "X-Nostr-Algorithm"
	AlgorithmNIP44       = "nip44"

	// pubkeyLogTruncateLen is the length to truncate pubkeys to in log messages
	pubkeyLogTruncateLen = 16
)

// truncatePubkey safely truncates a pubkey for logging purposes
func truncatePubkey(pubkey string) string {
	if len(pubkey) <= pubkeyLogTruncateLen {
		return pubkey
	}
	return pubkey[:pubkeyLogTruncateLen] + "..."
}

// Encryptor defines the interface for NIP-44 encryption/decryption
// This is implemented by NIP46Handler
type Encryptor interface {
	EncryptContent(ctx context.Context, userPubkey string, recipientPubkey string, plaintext string) (string, error)
	DecryptContent(ctx context.Context, userPubkey string, senderPubkey string, ciphertext string) (string, error)
}

// KeyResolver defines the interface for looking up Nostr pubkeys
type KeyResolver interface {
	// ResolvePubkey looks up the npub for an email address
	ResolvePubkey(ctx context.Context, email string) (string, error)
}

// EmailEncryptor handles email encryption/decryption using NIP-44
type EmailEncryptor struct {
	encryptor   Encryptor
	keyResolver KeyResolver
	logger      *zap.Logger
}

// NewEmailEncryptor creates a new email encryption handler
func NewEmailEncryptor(encryptor Encryptor, keyResolver KeyResolver, logger *zap.Logger) *EmailEncryptor {
	return &EmailEncryptor{
		encryptor:   encryptor,
		keyResolver: keyResolver,
		logger:      logger,
	}
}

// EncryptedEmail represents an email with encryption metadata
type EncryptedEmail struct {
	From            string
	To              string
	Subject         string
	Body            string // Encrypted body (base64)
	IsEncrypted     bool
	SenderPubkey    string
	RecipientPubkey string
	Algorithm       string
}

// EncryptEmailBody encrypts an email body for a recipient
func (e *EmailEncryptor) EncryptEmailBody(ctx context.Context, senderPubkey, recipientPubkey, plaintext string) (string, error) {
	e.logger.Debug("Encrypting email body",
		zap.String("sender", truncatePubkey(senderPubkey)),
		zap.String("recipient", truncatePubkey(recipientPubkey)))

	// Encrypt using NIP-44 via the bunker
	ciphertext, err := e.encryptor.EncryptContent(ctx, senderPubkey, recipientPubkey, plaintext)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt email body: %w", err)
	}

	return ciphertext, nil
}

// DecryptEmailBody decrypts an email body from a sender
func (e *EmailEncryptor) DecryptEmailBody(ctx context.Context, recipientPubkey, senderPubkey, ciphertext string) (string, error) {
	e.logger.Debug("Decrypting email body",
		zap.String("recipient", truncatePubkey(recipientPubkey)),
		zap.String("sender", truncatePubkey(senderPubkey)))

	// Decrypt using NIP-44 via the bunker
	plaintext, err := e.encryptor.DecryptContent(ctx, recipientPubkey, senderPubkey, ciphertext)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt email body: %w", err)
	}

	return plaintext, nil
}

// PrepareEncryptedEmail encrypts an email and returns it with metadata
func (e *EmailEncryptor) PrepareEncryptedEmail(ctx context.Context, from, to, subject, body, senderPubkey string) (*EncryptedEmail, error) {
	// Resolve recipient's pubkey from their email address
	recipientPubkey, err := e.keyResolver.ResolvePubkey(ctx, to)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve recipient pubkey for %s: %w", to, err)
	}

	// Encrypt the body
	encryptedBody, err := e.EncryptEmailBody(ctx, senderPubkey, recipientPubkey, body)
	if err != nil {
		return nil, err
	}

	return &EncryptedEmail{
		From:            from,
		To:              to,
		Subject:         subject,
		Body:            encryptedBody,
		IsEncrypted:     true,
		SenderPubkey:    senderPubkey,
		RecipientPubkey: recipientPubkey,
		Algorithm:       AlgorithmNIP44,
	}, nil
}

// FormatEncryptedEmailHeaders returns the X-Nostr-* headers as a map
func (e *EncryptedEmail) FormatEncryptedEmailHeaders() map[string]string {
	if !e.IsEncrypted {
		return nil
	}

	return map[string]string{
		HeaderNostrEncrypted: "true",
		HeaderNostrSender:    e.SenderPubkey,
		HeaderNostrRecipient: e.RecipientPubkey,
		HeaderNostrAlgorithm: e.Algorithm,
	}
}

// FormatRawEmail formats the encrypted email as raw RFC 5322 format
func (e *EncryptedEmail) FormatRawEmail() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("From: %s\r\n", e.From))
	sb.WriteString(fmt.Sprintf("To: %s\r\n", e.To))
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", e.Subject))
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: base64\r\n")

	if e.IsEncrypted {
		sb.WriteString(fmt.Sprintf("%s: true\r\n", HeaderNostrEncrypted))
		sb.WriteString(fmt.Sprintf("%s: %s\r\n", HeaderNostrSender, e.SenderPubkey))
		sb.WriteString(fmt.Sprintf("%s: %s\r\n", HeaderNostrRecipient, e.RecipientPubkey))
		sb.WriteString(fmt.Sprintf("%s: %s\r\n", HeaderNostrAlgorithm, e.Algorithm))
	}

	sb.WriteString("\r\n")

	// Encode body as base64 for transport (RFC 2045)
	// Note: if encrypted, Body contains the NIP-44 ciphertext which is already base64-like,
	// but we encode again for RFC compliance and consistency
	sb.WriteString(base64.StdEncoding.EncodeToString([]byte(e.Body)))

	return sb.String()
}

// EncryptionMetadata holds parsed encryption info from email headers
type EncryptionMetadata struct {
	IsEncrypted     bool
	SenderPubkey    string
	RecipientPubkey string
	Algorithm       string
}

// ParseEncryptedEmailHeaders extracts encryption metadata from email headers
func ParseEncryptedEmailHeaders(headers mail.Header) *EncryptionMetadata {
	encrypted := headers.Get(HeaderNostrEncrypted)
	if encrypted != "true" {
		return &EncryptionMetadata{IsEncrypted: false}
	}

	return &EncryptionMetadata{
		IsEncrypted:     true,
		SenderPubkey:    headers.Get(HeaderNostrSender),
		RecipientPubkey: headers.Get(HeaderNostrRecipient),
		Algorithm:       headers.Get(HeaderNostrAlgorithm),
	}
}

// ParseRawEmail parses a raw email and extracts encryption metadata
func ParseRawEmail(rawEmail string) (*EncryptionMetadata, string, error) {
	msg, err := mail.ReadMessage(strings.NewReader(rawEmail))
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse email: %w", err)
	}

	metadata := ParseEncryptedEmailHeaders(msg.Header)

	// Read body
	bodyBytes, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read email body: %w", err)
	}

	body := string(bodyBytes)

	// Decode base64 body if present
	if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body)); err == nil {
		body = string(decoded)
	}

	return metadata, body, nil
}

// DecryptEmail decrypts an encrypted email body using the metadata
func (e *EmailEncryptor) DecryptEmail(ctx context.Context, metadata *EncryptionMetadata, recipientPubkey, encryptedBody string) (string, error) {
	if !metadata.IsEncrypted {
		return encryptedBody, nil
	}

	if metadata.Algorithm != AlgorithmNIP44 {
		return "", fmt.Errorf("unsupported encryption algorithm: %s", metadata.Algorithm)
	}

	return e.DecryptEmailBody(ctx, recipientPubkey, metadata.SenderPubkey, encryptedBody)
}
