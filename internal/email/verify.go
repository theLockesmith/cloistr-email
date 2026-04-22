// Package email provides email signing and verification using Nostr keys.
package email

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/signing"
	"github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"
)

// Use constants from signing package
const (
	headerNostrPubkey        = signing.HeaderNostrPubkey
	headerNostrSig           = signing.HeaderNostrSig
	headerNostrSignedHeaders = signing.HeaderNostrSignedHeaders
)

// VerificationResult contains the result of email signature verification
type VerificationResult struct {
	// Signed indicates whether the email has Nostr signature headers
	Signed bool

	// Valid indicates whether the signature verified successfully
	Valid bool

	// Pubkey is the public key from the X-Nostr-Pubkey header
	Pubkey string

	// NIP05Verified indicates if the pubkey matches NIP-05 lookup
	NIP05Verified bool

	// NIP05Address is the email address used for NIP-05 lookup
	NIP05Address string

	// Reason provides details about verification failure
	Reason string

	// SignedHeaders lists which headers were signed
	SignedHeaders []string
}

// NIP05Resolver resolves email addresses to Nostr pubkeys via NIP-05
type NIP05Resolver interface {
	ResolvePubkey(ctx context.Context, email string) (string, error)
}

// EmailVerifier handles email signature verification
type EmailVerifier struct {
	nip05Resolver NIP05Resolver
	logger        *zap.Logger
}

// NewEmailVerifier creates a new email verifier
func NewEmailVerifier(nip05Resolver NIP05Resolver, logger *zap.Logger) *EmailVerifier {
	return &EmailVerifier{
		nip05Resolver: nip05Resolver,
		logger:        logger,
	}
}

// VerifiableEmail contains the data needed to verify an email signature
type VerifiableEmail struct {
	// Headers from the email (key -> value)
	Headers map[string]string

	// Body content
	Body string

	// NostrPubkey from X-Nostr-Pubkey header
	NostrPubkey string

	// NostrSig from X-Nostr-Sig header
	NostrSig string

	// NostrSignedHeaders from X-Nostr-Signed-Headers header
	NostrSignedHeaders string

	// FromAddress is the From header value (for NIP-05 verification)
	FromAddress string
}

// Verify verifies an email's Nostr signature
func (v *EmailVerifier) Verify(ctx context.Context, email *VerifiableEmail) *VerificationResult {
	result := &VerificationResult{}

	// Check if email has Nostr signature headers
	if email.NostrPubkey == "" || email.NostrSig == "" {
		result.Signed = false
		result.Reason = "no Nostr signature headers present"
		return result
	}

	result.Signed = true
	result.Pubkey = email.NostrPubkey

	// Parse signed headers list
	if email.NostrSignedHeaders == "" {
		result.Valid = false
		result.Reason = "missing X-Nostr-Signed-Headers"
		return result
	}

	signedHeaders := strings.Split(email.NostrSignedHeaders, ";")
	result.SignedHeaders = signedHeaders

	v.logger.Debug("Verifying email signature",
		zap.String("pubkey", truncateKey(email.NostrPubkey)),
		zap.Strings("signed_headers", signedHeaders))

	// Validate pubkey format
	if len(email.NostrPubkey) != 64 {
		result.Valid = false
		result.Reason = fmt.Sprintf("invalid pubkey length: %d (expected 64)", len(email.NostrPubkey))
		return result
	}

	if _, err := hex.DecodeString(email.NostrPubkey); err != nil {
		result.Valid = false
		result.Reason = "invalid pubkey: not valid hex"
		return result
	}

	// Validate signature format
	if len(email.NostrSig) != 128 {
		result.Valid = false
		result.Reason = fmt.Sprintf("invalid signature length: %d (expected 128)", len(email.NostrSig))
		return result
	}

	if _, err := hex.DecodeString(email.NostrSig); err != nil {
		result.Valid = false
		result.Reason = "invalid signature: not valid hex"
		return result
	}

	// Reconstruct canonical message
	canonical := v.canonicalize(email, signedHeaders)

	// Verify the Schnorr signature
	if !v.verifySchnorr(email.NostrPubkey, email.NostrSig, canonical) {
		result.Valid = false
		result.Reason = "signature verification failed"
		return result
	}

	result.Valid = true

	// Optionally verify NIP-05 if resolver is configured
	if v.nip05Resolver != nil && email.FromAddress != "" {
		// Extract email address from potential display name format
		emailAddr := extractEmailAddress(email.FromAddress)
		result.NIP05Address = emailAddr

		nip05Pubkey, err := v.nip05Resolver.ResolvePubkey(ctx, emailAddr)
		if err != nil {
			v.logger.Debug("NIP-05 lookup failed",
				zap.String("address", emailAddr),
				zap.Error(err))
			// Don't fail verification, just note NIP-05 wasn't verified
			result.NIP05Verified = false
		} else if nip05Pubkey == email.NostrPubkey {
			result.NIP05Verified = true
			v.logger.Debug("NIP-05 verification successful",
				zap.String("address", emailAddr))
		} else {
			result.NIP05Verified = false
			v.logger.Warn("NIP-05 pubkey mismatch",
				zap.String("address", emailAddr),
				zap.String("header_pubkey", truncateKey(email.NostrPubkey)),
				zap.String("nip05_pubkey", truncateKey(nip05Pubkey)))
		}
	}

	return result
}

// canonicalize recreates the canonical representation that was signed
func (v *EmailVerifier) canonicalize(email *VerifiableEmail, headersToVerify []string) []byte {
	var parts []string

	// Sort headers for deterministic ordering
	sortedHeaders := make([]string, len(headersToVerify))
	copy(sortedHeaders, headersToVerify)
	sort.Strings(sortedHeaders)

	// Normalize input headers to lowercase keys
	normalizedHeaders := make(map[string]string)
	for k, val := range email.Headers {
		normalizedHeaders[strings.ToLower(k)] = val
	}

	// Add each header in canonical form
	for _, name := range sortedHeaders {
		canonicalName := strings.ToLower(name)
		if value, ok := normalizedHeaders[canonicalName]; ok {
			// Trim value, normalize line endings
			canonicalValue := strings.TrimSpace(value)
			canonicalValue = strings.ReplaceAll(canonicalValue, "\r\n", "\n")
			canonicalValue = strings.ReplaceAll(canonicalValue, "\r", "\n")

			parts = append(parts, fmt.Sprintf("%s:%s", canonicalName, canonicalValue))
		}
	}

	// Join headers with newline
	headerBlock := strings.Join(parts, "\n")

	// Canonicalize body
	bodyCanonical := v.canonicalizeBody(email.Body)

	// Hash the body
	bodyHash := sha256.Sum256([]byte(bodyCanonical))

	// Combine headers and body hash
	combined := append([]byte(headerBlock+"\n"), bodyHash[:]...)

	return combined
}

// canonicalizeBody applies canonicalization rules to the email body
func (v *EmailVerifier) canonicalizeBody(body string) string {
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

// verifySchnorr verifies a BIP-340 Schnorr signature
func (v *EmailVerifier) verifySchnorr(pubkeyHex, sigHex string, message []byte) bool {
	// Hash the message (same as signing)
	messageHash := sha256.Sum256(message)

	// Reconstruct the same deterministic event used for signing
	// Must match the parameters used in LocalSigner.Sign()
	event := nostr.Event{
		PubKey:    pubkeyHex,
		CreatedAt: 0, // Fixed timestamp (same as signing)
		Kind:      27235, // Email signature event kind
		Tags:      nostr.Tags{},
		Content:   hex.EncodeToString(messageHash[:]),
		Sig:       sigHex,
	}

	// go-nostr's CheckSignature verifies against the computed event ID
	valid, err := event.CheckSignature()
	if err != nil {
		v.logger.Debug("Signature check error", zap.Error(err))
		return false
	}

	return valid
}

// VerifyEmail is a convenience function to verify an email
func VerifyEmail(ctx context.Context, headers map[string]string, body string, nip05Resolver NIP05Resolver, logger *zap.Logger) *VerificationResult {
	verifier := NewEmailVerifier(nip05Resolver, logger)

	// Normalize header keys to lowercase for lookup
	normalizedHeaders := make(map[string]string)
	for k, val := range headers {
		normalizedHeaders[strings.ToLower(k)] = val
	}

	// Extract Nostr headers
	email := &VerifiableEmail{
		Headers:            normalizedHeaders,
		Body:               body,
		NostrPubkey:        normalizedHeaders[strings.ToLower(HeaderNostrPubkey)],
		NostrSig:           normalizedHeaders[strings.ToLower(HeaderNostrSig)],
		NostrSignedHeaders: normalizedHeaders[strings.ToLower(HeaderNostrSignedHeaders)],
		FromAddress:        extractEmailAddress(normalizedHeaders["from"]),
	}

	return verifier.Verify(ctx, email)
}

// extractEmailAddress extracts the email address from a From header value
// e.g., "Alice <alice@example.com>" -> "alice@example.com"
func extractEmailAddress(from string) string {
	from = strings.TrimSpace(from)

	// Check for angle bracket format: "Name <email@example.com>"
	if start := strings.Index(from, "<"); start != -1 {
		if end := strings.Index(from, ">"); end > start {
			return strings.TrimSpace(from[start+1 : end])
		}
	}

	// Assume it's just the email address
	return from
}
