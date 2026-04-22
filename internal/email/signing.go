// Package email provides email signing and verification using Nostr keys.
// This implements RFC-002: Nostr as the Identity Layer for SMTP.
package email

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/signing"
	"go.uber.org/zap"
)

// Re-export signing package types and constants for convenience
type Signer = signing.Signer
type SignResult = signing.SignResult

var (
	NewLocalSigner = signing.NewLocalSigner
)

const (
	HeaderNostrPubkey        = signing.HeaderNostrPubkey
	HeaderNostrSig           = signing.HeaderNostrSig
	HeaderNostrSignedHeaders = signing.HeaderNostrSignedHeaders
)

// DefaultSignedHeaders are the headers that should always be signed
var DefaultSignedHeaders = []string{
	"from",
	"to",
	"date",
	"message-id",
	"subject",
}

// OptionalSignedHeaders are additional headers that are signed if present
var OptionalSignedHeaders = []string{
	"cc",
	"in-reply-to",
	"references",
}

// EmailSigner handles email signing operations
type EmailSigner struct {
	logger *zap.Logger
}

// NewEmailSigner creates a new email signer
func NewEmailSigner(logger *zap.Logger) *EmailSigner {
	return &EmailSigner{
		logger: logger,
	}
}

// SignableEmail contains the data needed to sign an email
type SignableEmail struct {
	// Headers to sign (key -> value)
	Headers map[string]string

	// Body content (will be hashed)
	Body string

	// MessageID for the email
	MessageID string

	// Date header value
	Date string
}

// Sign signs an email and returns the signature data
func (s *EmailSigner) Sign(ctx context.Context, email *SignableEmail, signer Signer) (*SignResult, error) {
	s.logger.Debug("Signing email",
		zap.String("message_id", email.MessageID),
		zap.String("pubkey", truncateKey(signer.PublicKey())))

	// Determine which headers to sign
	headersToSign := s.selectHeadersToSign(email.Headers)

	// Canonicalize the email
	canonical := s.canonicalize(email, headersToSign)

	// Sign the canonical data
	sig, err := signer.Sign(ctx, canonical)
	if err != nil {
		return nil, fmt.Errorf("failed to sign email: %w", err)
	}

	return &SignResult{
		Signature:     sig,
		Pubkey:        signer.PublicKey(),
		SignedHeaders: strings.Join(headersToSign, ";"),
		CanonicalData: canonical,
	}, nil
}

// selectHeadersToSign determines which headers should be signed
func (s *EmailSigner) selectHeadersToSign(headers map[string]string) []string {
	var toSign []string

	// Always include default headers if present
	for _, h := range DefaultSignedHeaders {
		if _, ok := headers[h]; ok {
			toSign = append(toSign, h)
		}
	}

	// Include optional headers if present
	for _, h := range OptionalSignedHeaders {
		if _, ok := headers[h]; ok {
			toSign = append(toSign, h)
		}
	}

	// Sort for deterministic ordering
	sort.Strings(toSign)

	return toSign
}

// canonicalize creates the canonical representation of the email for signing
// Following RFC-002 canonicalization rules:
// 1. Header names lowercased
// 2. Header values trimmed of leading/trailing whitespace
// 3. Line endings normalized to \n
// 4. Headers sorted alphabetically
// 5. Body whitespace at end of lines trimmed
// 6. Trailing blank lines removed
func (s *EmailSigner) canonicalize(email *SignableEmail, headersToSign []string) []byte {
	var parts []string

	// Sort headers for deterministic ordering
	sortedHeaders := make([]string, len(headersToSign))
	copy(sortedHeaders, headersToSign)
	sort.Strings(sortedHeaders)

	// Add each header in canonical form
	for _, name := range sortedHeaders {
		if value, ok := email.Headers[name]; ok {
			// Lowercase name, trim value, normalize line endings
			canonicalName := strings.ToLower(name)
			canonicalValue := strings.TrimSpace(value)
			canonicalValue = strings.ReplaceAll(canonicalValue, "\r\n", "\n")
			canonicalValue = strings.ReplaceAll(canonicalValue, "\r", "\n")

			parts = append(parts, fmt.Sprintf("%s:%s", canonicalName, canonicalValue))
		}
	}

	// Join headers with newline
	headerBlock := strings.Join(parts, "\n")

	// Canonicalize body
	bodyCanonical := s.canonicalizeBody(email.Body)

	// Hash the body
	bodyHash := sha256.Sum256([]byte(bodyCanonical))

	// Combine headers and body hash
	combined := append([]byte(headerBlock+"\n"), bodyHash[:]...)

	return combined
}

// canonicalizeBody applies canonicalization rules to the email body
func (s *EmailSigner) canonicalizeBody(body string) string {
	// Normalize line endings
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")

	// Trim whitespace at end of each line
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}

	// Remove trailing blank lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n")
}

// AddSignatureHeaders adds the X-Nostr-* signature headers to a header map
func (s *EmailSigner) AddSignatureHeaders(headers map[string]string, result *SignResult) {
	headers[HeaderNostrPubkey] = result.Pubkey
	headers[HeaderNostrSig] = result.Signature
	headers[HeaderNostrSignedHeaders] = result.SignedHeaders
}

// truncateKey truncates a key for logging
func truncateKey(key string) string {
	if len(key) <= 16 {
		return key
	}
	return key[:16] + "..."
}

// SignEmail is a convenience function to sign an email with all the common headers
func SignEmail(ctx context.Context, signer Signer, headers map[string]string, body string, logger *zap.Logger) (*SignResult, error) {
	emailSigner := NewEmailSigner(logger)

	// Normalize header keys to lowercase for lookup
	normalizedHeaders := make(map[string]string)
	for k, v := range headers {
		normalizedHeaders[strings.ToLower(k)] = v
	}

	signable := &SignableEmail{
		Headers:   normalizedHeaders,
		Body:      body,
		MessageID: normalizedHeaders["message-id"],
		Date:      normalizedHeaders["date"],
	}

	return emailSigner.Sign(ctx, signable, signer)
}
