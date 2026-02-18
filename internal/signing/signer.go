// Package signing provides Nostr email signing interfaces and types.
// This package contains only interfaces to avoid import cycles between
// transport and email packages.
package signing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/nbd-wtf/go-nostr"
)

// Signer defines the interface for Nostr signing operations
type Signer interface {
	// Sign signs a message with the user's Nostr key
	// Returns the 64-byte Schnorr signature as hex
	Sign(ctx context.Context, message []byte) (string, error)

	// PublicKey returns the signer's public key as hex
	PublicKey() string
}

// LocalSigner implements Signer using a local private key
// Used for testing and local signing scenarios
type LocalSigner struct {
	privateKey string
	publicKey  string
}

// NewLocalSigner creates a signer from a private key hex string
func NewLocalSigner(privateKeyHex string) (*LocalSigner, error) {
	if privateKeyHex == "" {
		return nil, fmt.Errorf("private key cannot be empty")
	}
	if len(privateKeyHex) != 64 {
		return nil, fmt.Errorf("invalid private key length: expected 64 hex chars, got %d", len(privateKeyHex))
	}

	pubkey, err := nostr.GetPublicKey(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("failed to derive public key: %w", err)
	}

	// Verify the pubkey is non-zero
	if pubkey == "0000000000000000000000000000000000000000000000000000000000000000" {
		return nil, fmt.Errorf("invalid private key: produces zero public key")
	}

	return &LocalSigner{
		privateKey: privateKeyHex,
		publicKey:  pubkey,
	}, nil
}

// Sign signs a message using Schnorr signature (BIP-340)
func (s *LocalSigner) Sign(ctx context.Context, message []byte) (string, error) {
	// Hash the message
	messageHash := sha256.Sum256(message)

	// Create a deterministic Nostr event for signing
	// We use fixed values for kind, created_at, and tags so verification can reconstruct
	// the same event ID from just the message hash
	event := nostr.Event{
		PubKey:    s.publicKey,
		CreatedAt: 0, // Fixed timestamp for deterministic event ID
		Kind:      27235, // Email signature event kind
		Tags:      nostr.Tags{},
		Content:   hex.EncodeToString(messageHash[:]),
	}

	if err := event.Sign(s.privateKey); err != nil {
		return "", fmt.Errorf("signing failed: %w", err)
	}

	return event.Sig, nil
}

// PublicKey returns the signer's public key
func (s *LocalSigner) PublicKey() string {
	return s.publicKey
}

// SignResult contains the signature and metadata
type SignResult struct {
	// Signature is the 64-byte Schnorr signature as hex
	Signature string

	// Pubkey is the signer's public key as hex
	Pubkey string

	// SignedHeaders is the semicolon-separated list of signed header names
	SignedHeaders string

	// CanonicalData is the data that was signed (for debugging)
	CanonicalData []byte
}

// Header names for Nostr signing metadata
const (
	HeaderNostrPubkey        = "X-Nostr-Pubkey"
	HeaderNostrSig           = "X-Nostr-Sig"
	HeaderNostrSignedHeaders = "X-Nostr-Signed-Headers"
)
