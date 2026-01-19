// Package encryption provides email encryption using NIP-44.
// This file defines the signer abstraction that supports both
// NIP-46 (server-side) and NIP-07 (client-side) encryption modes.
package encryption

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// SignerType identifies the encryption mechanism
type SignerType string

const (
	// SignerTypeNIP46 uses a remote signer (nsecbunker) for server-side encryption
	// The server has access to the user's signing capability via NIP-46 protocol
	SignerTypeNIP46 SignerType = "nip46"

	// SignerTypeNIP07 indicates client-side encryption via browser extension
	// The server never sees plaintext - client encrypts before sending
	SignerTypeNIP07 SignerType = "nip07"

	// SignerTypeNone indicates no encryption capability
	SignerTypeNone SignerType = "none"
)

// EncryptionMode describes how a message should be/was encrypted
type EncryptionMode string

const (
	// ModeNone - no encryption
	ModeNone EncryptionMode = "none"

	// ModeServerSide - server encrypts using NIP-46 signer
	ModeServerSide EncryptionMode = "server"

	// ModeClientSide - client pre-encrypted using NIP-07
	// Server stores ciphertext without decryption capability
	ModeClientSide EncryptionMode = "client"
)

// Signer defines the interface for cryptographic operations.
// Implementations include NIP-46 remote signers and stub signers for testing.
type Signer interface {
	// Type returns the signer type
	Type() SignerType

	// Pubkey returns the user's public key (hex)
	Pubkey() string

	// Encrypt encrypts plaintext for a recipient's public key
	// Uses NIP-44 encryption
	Encrypt(ctx context.Context, plaintext string, recipientPubkey string) (string, error)

	// Decrypt decrypts ciphertext from a sender's public key
	// Uses NIP-44 decryption
	Decrypt(ctx context.Context, ciphertext string, senderPubkey string) (string, error)

	// CanEncrypt returns true if this signer can perform encryption
	// NIP-07 signers return false - they indicate client handles encryption
	CanEncrypt() bool

	// CanDecrypt returns true if this signer can perform decryption
	// NIP-07 signers return false - they indicate client handles decryption
	CanDecrypt() bool
}

// ClientSideSigner is a marker signer for NIP-07 mode.
// It doesn't perform any cryptographic operations - it indicates
// that the client is responsible for encryption/decryption.
type ClientSideSigner struct {
	pubkey string
}

// NewClientSideSigner creates a signer for NIP-07 mode
func NewClientSideSigner(pubkey string) *ClientSideSigner {
	return &ClientSideSigner{pubkey: pubkey}
}

func (s *ClientSideSigner) Type() SignerType {
	return SignerTypeNIP07
}

func (s *ClientSideSigner) Pubkey() string {
	return s.pubkey
}

func (s *ClientSideSigner) Encrypt(ctx context.Context, plaintext string, recipientPubkey string) (string, error) {
	return "", ErrClientSideOnly
}

func (s *ClientSideSigner) Decrypt(ctx context.Context, ciphertext string, senderPubkey string) (string, error) {
	return "", ErrClientSideOnly
}

func (s *ClientSideSigner) CanEncrypt() bool {
	return false
}

func (s *ClientSideSigner) CanDecrypt() bool {
	return false
}

// SignerStore manages signers for users
type SignerStore interface {
	// GetSigner retrieves the signer for a user
	GetSigner(ctx context.Context, npub string) (Signer, error)

	// SetSignerType sets the preferred signer type for a user
	SetSignerType(ctx context.Context, npub string, signerType SignerType) error
}

// EncryptionRequest contains the parameters for encrypting a message
type EncryptionRequest struct {
	// Plaintext to encrypt (ignored if PreEncrypted is set)
	Plaintext string

	// PreEncrypted ciphertext (for NIP-07 mode)
	// If set, Plaintext is ignored and this is used directly
	PreEncrypted string

	// SenderPubkey is the sender's public key
	SenderPubkey string

	// RecipientPubkey is the recipient's public key
	RecipientPubkey string

	// Mode indicates the encryption mode to use
	Mode EncryptionMode
}

// EncryptionResult contains the result of encryption
type EncryptionResult struct {
	// Ciphertext is the encrypted data
	Ciphertext string

	// Mode indicates how encryption was performed
	Mode EncryptionMode

	// Algorithm identifier (e.g., "nip44-v2")
	Algorithm string
}

// EncryptionService coordinates encryption across different modes
type EncryptionService struct {
	signerStore SignerStore
	logger      *zap.Logger
}

// NewEncryptionService creates a new encryption service
func NewEncryptionService(signerStore SignerStore, logger *zap.Logger) *EncryptionService {
	return &EncryptionService{
		signerStore: signerStore,
		logger:      logger,
	}
}

// EncryptForRecipient encrypts a message for a recipient
func (s *EncryptionService) EncryptForRecipient(ctx context.Context, req *EncryptionRequest) (*EncryptionResult, error) {
	// If pre-encrypted (NIP-07 mode), validate and pass through
	if req.PreEncrypted != "" {
		if req.Mode != ModeClientSide {
			return nil, errors.New("pre-encrypted content requires client-side mode")
		}

		s.logger.Debug("Using pre-encrypted content (NIP-07 mode)",
			zap.String("recipient", truncatePubkey(req.RecipientPubkey)))

		return &EncryptionResult{
			Ciphertext: req.PreEncrypted,
			Mode:       ModeClientSide,
			Algorithm:  "nip44-v2-client",
		}, nil
	}

	// Server-side encryption - need a signer
	if req.Mode != ModeServerSide {
		return nil, errors.New("plaintext encryption requires server-side mode")
	}

	signer, err := s.signerStore.GetSigner(ctx, req.SenderPubkey)
	if err != nil {
		return nil, fmt.Errorf("failed to get signer: %w", err)
	}

	if !signer.CanEncrypt() {
		return nil, ErrSignerCannotEncrypt
	}

	ciphertext, err := signer.Encrypt(ctx, req.Plaintext, req.RecipientPubkey)
	if err != nil {
		return nil, fmt.Errorf("encryption failed: %w", err)
	}

	return &EncryptionResult{
		Ciphertext: ciphertext,
		Mode:       ModeServerSide,
		Algorithm:  "nip44-v2",
	}, nil
}

// DecryptionRequest contains the parameters for decrypting a message
type DecryptionRequest struct {
	// Ciphertext to decrypt
	Ciphertext string

	// RecipientPubkey is the recipient's public key (who is decrypting)
	RecipientPubkey string

	// SenderPubkey is the sender's public key
	SenderPubkey string

	// Mode indicates how the message was encrypted
	Mode EncryptionMode
}

// DecryptionResult contains the result of decryption
type DecryptionResult struct {
	// Plaintext is the decrypted data (empty if client-side only)
	Plaintext string

	// RequiresClientDecryption is true if the client must decrypt
	RequiresClientDecryption bool

	// Ciphertext is passed through for client-side decryption
	Ciphertext string
}

// DecryptForRecipient decrypts a message for a recipient
func (s *EncryptionService) DecryptForRecipient(ctx context.Context, req *DecryptionRequest) (*DecryptionResult, error) {
	// If client-side encrypted, we can't decrypt - return ciphertext for client
	if req.Mode == ModeClientSide {
		s.logger.Debug("Message requires client-side decryption (NIP-07 mode)",
			zap.String("recipient", truncatePubkey(req.RecipientPubkey)))

		return &DecryptionResult{
			RequiresClientDecryption: true,
			Ciphertext:               req.Ciphertext,
		}, nil
	}

	// Server-side decryption - need a signer
	signer, err := s.signerStore.GetSigner(ctx, req.RecipientPubkey)
	if err != nil {
		return nil, fmt.Errorf("failed to get signer: %w", err)
	}

	if !signer.CanDecrypt() {
		// User has switched to NIP-07 mode but message was server-encrypted
		// They need to use NIP-46 to decrypt
		return nil, ErrSignerCannotDecrypt
	}

	plaintext, err := signer.Decrypt(ctx, req.Ciphertext, req.SenderPubkey)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return &DecryptionResult{
		Plaintext:                plaintext,
		RequiresClientDecryption: false,
	}, nil
}

// DetermineEncryptionMode determines the best encryption mode for a user
func (s *EncryptionService) DetermineEncryptionMode(ctx context.Context, npub string) (EncryptionMode, error) {
	signer, err := s.signerStore.GetSigner(ctx, npub)
	if err != nil {
		return ModeNone, fmt.Errorf("failed to get signer: %w", err)
	}

	switch signer.Type() {
	case SignerTypeNIP46:
		return ModeServerSide, nil
	case SignerTypeNIP07:
		return ModeClientSide, nil
	default:
		return ModeNone, nil
	}
}

// Signer errors
var (
	// ErrClientSideOnly is returned when trying to use NIP-07 signer for server-side ops
	ErrClientSideOnly = errors.New("this signer requires client-side encryption/decryption")

	// ErrSignerCannotEncrypt is returned when the signer can't encrypt
	ErrSignerCannotEncrypt = errors.New("signer cannot perform encryption")

	// ErrSignerCannotDecrypt is returned when the signer can't decrypt
	ErrSignerCannotDecrypt = errors.New("signer cannot perform decryption - message may require original encryption method")
)
