package auth

import (
	"context"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"github.com/nbd-wtf/go-nostr"
)

// nip46Signer adapts a NIP46Handler + a bound user pubkey to the
// encryption.Signer interface, delegating crypto operations to the user's
// remote bunker.
type nip46Signer struct {
	handler    *NIP46Handler
	userPubkey string // hex
}

func (s *nip46Signer) Type() encryption.SignerType { return encryption.SignerTypeNIP46 }

func (s *nip46Signer) Pubkey() string { return s.userPubkey }

func (s *nip46Signer) Encrypt(ctx context.Context, plaintext, recipientPubkey string) (string, error) {
	return s.handler.EncryptContent(ctx, s.userPubkey, recipientPubkey, plaintext)
}

func (s *nip46Signer) Decrypt(ctx context.Context, ciphertext, senderPubkey string) (string, error) {
	return s.handler.DecryptContent(ctx, s.userPubkey, senderPubkey, ciphertext)
}

func (s *nip46Signer) CanEncrypt() bool { return true }

func (s *nip46Signer) CanDecrypt() bool { return true }

func (s *nip46Signer) SignEvent(ctx context.Context, event *nostr.Event) error {
	return s.handler.SignEvent(ctx, s.userPubkey, event)
}

// NIP46SignerStore implements encryption.SignerStore backed by a NIP46Handler.
// Each GetSigner returns a signer bound to the requested user pubkey; the
// actual presence of a live bunker session is checked lazily when an operation
// runs (Encrypt/Decrypt/SignEvent return the handler's error if absent).
type NIP46SignerStore struct {
	handler *NIP46Handler
}

// NewNIP46SignerStore creates a SignerStore over the given NIP-46 handler.
func NewNIP46SignerStore(handler *NIP46Handler) *NIP46SignerStore {
	return &NIP46SignerStore{handler: handler}
}

// GetSigner returns a NIP-46-backed signer bound to the user pubkey (hex).
func (st *NIP46SignerStore) GetSigner(ctx context.Context, pubkey string) (encryption.Signer, error) {
	return &nip46Signer{handler: st.handler, userPubkey: pubkey}, nil
}

// SetSignerType is a no-op: signer type is fixed to NIP-46 for this store.
// (Client-side/NIP-07 users never reach the server signer.)
func (st *NIP46SignerStore) SetSignerType(ctx context.Context, pubkey string, signerType encryption.SignerType) error {
	return nil
}
