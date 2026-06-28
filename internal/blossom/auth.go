// Package blossom implements a client for Blossom blob storage servers
// (BUD-01/02), used to offload NIP-44 encrypted email content out of
// PostgreSQL onto user-chosen, content-addressed storage.
//
// See docs/003-blossom-storage.md (RFC-003) for the architecture.
package blossom

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// authEventKind is the Nostr event kind used for Blossom authorization
// (BUD-01: "Authorization events", kind 24242).
const authEventKind = 24242

// AuthSigner produces signed kind-24242 authorization events for Blossom
// requests. It is intentionally decoupled from how the key is held: a local
// key (server-side mode), a NIP-46 bunker, or a NIP-07 browser extension can
// each satisfy it.
type AuthSigner interface {
	// SignAuth builds and signs a kind-24242 authorization event for the
	// given verb ("upload", "get", "delete") covering the provided blob
	// hashes (sha256 hex), valid until expiration.
	SignAuth(ctx context.Context, verb string, hashes []string, expiration time.Time) (*nostr.Event, error)

	// PublicKey returns the signer's public key as hex.
	PublicKey() string
}

// LocalAuthSigner signs authorization events with a locally held private key.
// Used for server-side encryption mode and tests.
type LocalAuthSigner struct {
	privateKey string
	publicKey  string
}

// NewLocalAuthSigner creates a signer from a 64-char hex private key.
func NewLocalAuthSigner(privateKeyHex string) (*LocalAuthSigner, error) {
	if len(privateKeyHex) != 64 {
		return nil, fmt.Errorf("invalid private key length: expected 64 hex chars, got %d", len(privateKeyHex))
	}
	pubkey, err := nostr.GetPublicKey(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("failed to derive public key: %w", err)
	}
	return &LocalAuthSigner{privateKey: privateKeyHex, publicKey: pubkey}, nil
}

// SignAuth implements AuthSigner.
func (s *LocalAuthSigner) SignAuth(ctx context.Context, verb string, hashes []string, expiration time.Time) (*nostr.Event, error) {
	event := buildAuthEvent(s.publicKey, verb, hashes, expiration)
	if err := event.Sign(s.privateKey); err != nil {
		return nil, fmt.Errorf("signing auth event failed: %w", err)
	}
	return event, nil
}

// PublicKey implements AuthSigner.
func (s *LocalAuthSigner) PublicKey() string { return s.publicKey }

// String redacts the private key when the signer is formatted (e.g. %v/%+v),
// so it cannot leak into logs.
func (s *LocalAuthSigner) String() string { return "LocalAuthSigner<redacted>" }

// GoString redacts the private key under %#v formatting.
func (s *LocalAuthSigner) GoString() string { return "LocalAuthSigner<redacted>" }

// EventSignFunc signs a prepared Nostr event in place (sets ID, pubkey, sig).
// It is satisfied by a NIP-46 bunker (encryption.Signer.SignEvent), a NIP-07
// bridge, or a local key — keeping this package free of those dependencies.
type EventSignFunc func(ctx context.Context, event *nostr.Event) error

// eventAuthSigner builds the kind-24242 event and delegates signing to an
// injected EventSignFunc, signing as the given public key.
type eventAuthSigner struct {
	pubkey string
	sign   EventSignFunc
}

// NewEventAuthSigner returns an AuthSigner that authorizes Blossom requests as
// pubkey, signing the authorization events via sign (e.g. a user's NIP-46
// bunker). This is how the email service authorizes uploads as the user.
func NewEventAuthSigner(pubkey string, sign EventSignFunc) AuthSigner {
	return &eventAuthSigner{pubkey: pubkey, sign: sign}
}

// SignAuth implements AuthSigner.
func (s *eventAuthSigner) SignAuth(ctx context.Context, verb string, hashes []string, expiration time.Time) (*nostr.Event, error) {
	event := buildAuthEvent(s.pubkey, verb, hashes, expiration)
	if err := s.sign(ctx, event); err != nil {
		return nil, fmt.Errorf("signing auth event failed: %w", err)
	}
	return event, nil
}

// PublicKey implements AuthSigner.
func (s *eventAuthSigner) PublicKey() string { return s.pubkey }

// buildAuthEvent assembles the unsigned kind-24242 event for a verb + hashes.
func buildAuthEvent(pubkey, verb string, hashes []string, expiration time.Time) *nostr.Event {
	tags := nostr.Tags{
		nostr.Tag{"t", verb},
		nostr.Tag{"expiration", fmt.Sprintf("%d", expiration.Unix())},
	}
	for _, h := range hashes {
		tags = append(tags, nostr.Tag{"x", h})
	}
	return &nostr.Event{
		PubKey:    pubkey,
		CreatedAt: nostr.Now(),
		Kind:      authEventKind,
		Tags:      tags,
		Content:   fmt.Sprintf("%s blob", verb),
	}
}

// authorizationHeader encodes a signed auth event as a Blossom
// "Authorization: Nostr <base64-event>" header value.
func authorizationHeader(event *nostr.Event) (string, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("marshaling auth event: %w", err)
	}
	return "Nostr " + base64.StdEncoding.EncodeToString(raw), nil
}
