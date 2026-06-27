package email

import (
	"context"
	"errors"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"
)

// fakeSigner implements encryption.Signer with a trivial reversible "encryption"
// (prefixing) so the at-rest routing can be tested without a real bunker.
type fakeSigner struct {
	pubkey  string
	failEnc bool
	canEnc  bool
}

func (f *fakeSigner) Type() encryption.SignerType { return encryption.SignerTypeNIP46 }
func (f *fakeSigner) Pubkey() string              { return f.pubkey }
func (f *fakeSigner) Encrypt(ctx context.Context, plaintext, recipientPubkey string) (string, error) {
	if f.failEnc {
		return "", errors.New("no bunker session")
	}
	return "enc(" + plaintext + ")->" + recipientPubkey, nil
}
func (f *fakeSigner) Decrypt(ctx context.Context, ciphertext, senderPubkey string) (string, error) {
	return ciphertext, nil
}
func (f *fakeSigner) CanEncrypt() bool                                        { return f.canEnc }
func (f *fakeSigner) CanDecrypt() bool                                        { return true }
func (f *fakeSigner) SignEvent(ctx context.Context, event *nostr.Event) error { return nil }

type fakeSignerStore struct{ signer *fakeSigner }

func (s fakeSignerStore) GetSigner(ctx context.Context, npub string) (encryption.Signer, error) {
	return s.signer, nil
}
func (s fakeSignerStore) SetSignerType(ctx context.Context, npub string, t encryption.SignerType) error {
	return nil
}

func newServiceWithSigner(signer *fakeSigner) *Service {
	var encSvc *encryption.EncryptionService
	if signer != nil {
		encSvc = encryption.NewEncryptionService(fakeSignerStore{signer: signer}, zap.NewNop())
	}
	return NewService(nil, nil, encSvc, nil, zap.NewNop())
}

func TestStoredMode(t *testing.T) {
	sp := func(s string) *string { return &s }
	cases := []struct {
		name string
		in   *storage.Email
		want encryption.EncryptionMode
	}{
		{"explicit server", &storage.Email{EncryptionMode: sp("server"), IsEncrypted: true}, encryption.ModeServerSide},
		{"explicit client", &storage.Email{EncryptionMode: sp("client"), IsEncrypted: true}, encryption.ModeClientSide},
		{"explicit none", &storage.Email{EncryptionMode: sp("none")}, encryption.ModeNone},
		{"legacy encrypted -> client", &storage.Email{EncryptionMode: nil, IsEncrypted: true}, encryption.ModeClientSide},
		{"legacy plaintext -> none", &storage.Email{EncryptionMode: nil, IsEncrypted: false}, encryption.ModeNone},
		{"empty string -> falls back", &storage.Email{EncryptionMode: sp(""), IsEncrypted: false}, encryption.ModeNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := storedMode(tc.in); got != tc.want {
				t.Errorf("storedMode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBodyAtRest_None(t *testing.T) {
	s := newServiceWithSigner(nil)
	body, mode := s.bodyAtRest(context.Background(), &SendRequest{
		EncryptionMode: encryption.ModeNone,
		Body:           "hello world",
	})
	if body != "hello world" || mode != "none" {
		t.Errorf("got (%q,%q), want (hello world, none)", body, mode)
	}
}

func TestBodyAtRest_Client(t *testing.T) {
	s := newServiceWithSigner(nil)
	body, mode := s.bodyAtRest(context.Background(), &SendRequest{
		EncryptionMode:   encryption.ModeClientSide,
		PreEncryptedBody: "CIPHERTEXT",
		Body:             "should be ignored",
	})
	if body != "CIPHERTEXT" || mode != "client" {
		t.Errorf("got (%q,%q), want (CIPHERTEXT, client)", body, mode)
	}
}

func TestBodyAtRest_ServerEncrypts(t *testing.T) {
	s := newServiceWithSigner(&fakeSigner{pubkey: "senderpub", canEnc: true})
	body, mode := s.bodyAtRest(context.Background(), &SendRequest{
		EncryptionMode: encryption.ModeServerSide,
		SenderNpub:     "senderpub",
		Body:           "secret",
	})
	if mode != "server" {
		t.Fatalf("mode = %q, want server", mode)
	}
	// Self-encrypted: recipient pubkey is the sender's own key, and the body
	// must NOT be the plaintext.
	if body == "secret" || body == "" {
		t.Fatalf("body not encrypted at rest: %q", body)
	}
	if body != "enc(secret)->senderpub" {
		t.Errorf("unexpected ciphertext: %q", body)
	}
}

func TestBodyAtRest_ServerDropsBodyOnFailure(t *testing.T) {
	// Encryption failure must drop the body, never persist plaintext.
	s := newServiceWithSigner(&fakeSigner{pubkey: "senderpub", canEnc: true, failEnc: true})
	body, mode := s.bodyAtRest(context.Background(), &SendRequest{
		EncryptionMode: encryption.ModeServerSide,
		SenderNpub:     "senderpub",
		Body:           "secret",
	})
	if mode != "server" {
		t.Errorf("mode = %q, want server", mode)
	}
	if body != "" {
		t.Errorf("expected body dropped on failure, got %q (plaintext leak risk)", body)
	}
}

func TestBodyAtRest_ServerNoEncryptionService(t *testing.T) {
	s := newServiceWithSigner(nil) // encryptionSvc == nil
	body, mode := s.bodyAtRest(context.Background(), &SendRequest{
		EncryptionMode: encryption.ModeServerSide,
		SenderNpub:     "senderpub",
		Body:           "secret",
	})
	if body != "" || mode != "server" {
		t.Errorf("got (%q,%q), want (\"\", server) — no plaintext when svc missing", body, mode)
	}
}
