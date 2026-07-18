package email

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/identity"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	_ "github.com/lib/pq"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
	"go.uber.org/zap"
)

// This is a real end-to-end test of the server-side encryption round trip
// against a live PostgreSQL instance. It exercises the actual Service.bodyAtRest
// (encrypt-at-rest), storage.CreateEmail/GetEmail (real INSERT/SELECT incl. the
// encryption_mode column), and Service.GetEmail (decrypt-by-stored-mode).
//
// SUBSTITUTION: the remote NIP-46 bunker is replaced with a local-key signer
// that performs REAL NIP-44 (not a stub). The encryption.Signer interface is
// the boundary; this proves the Service uses it correctly with genuine crypto.
// It does NOT exercise the live bunker RPC, the SMTP wire, or the HTTP layer.
//
// Run with: DATABASE_URL=postgres://postgres:postgres@127.0.0.1:5432/cloistr_email_e2e?sslmode=disable go test ./internal/email/ -run E2E -v

// localNIP44Signer is a real-crypto encryption.Signer backed by a local key.
type localNIP44Signer struct {
	sk     string
	pubkey string
}

func (s *localNIP44Signer) Type() encryption.SignerType { return encryption.SignerTypeNIP46 }
func (s *localNIP44Signer) Pubkey() string              { return s.pubkey }
func (s *localNIP44Signer) CanEncrypt() bool            { return true }
func (s *localNIP44Signer) CanDecrypt() bool            { return true }

func (s *localNIP44Signer) Encrypt(ctx context.Context, plaintext, recipientPubkey string) (string, error) {
	ck, err := nip44.GenerateConversationKey(recipientPubkey, s.sk)
	if err != nil {
		return "", err
	}
	return nip44.Encrypt(plaintext, ck)
}

func (s *localNIP44Signer) Decrypt(ctx context.Context, ciphertext, senderPubkey string) (string, error) {
	ck, err := nip44.GenerateConversationKey(senderPubkey, s.sk)
	if err != nil {
		return "", err
	}
	return nip44.Decrypt(ciphertext, ck)
}

func (s *localNIP44Signer) SignEvent(ctx context.Context, event *nostr.Event) error {
	return event.Sign(s.sk)
}

// localSignerStore returns the same local signer for any pubkey lookup.
type localSignerStore struct{ signer *localNIP44Signer }

func (st localSignerStore) GetSigner(ctx context.Context, pubkey string) (encryption.Signer, error) {
	return st.signer, nil
}
func (st localSignerStore) SetSignerType(ctx context.Context, pubkey string, t encryption.SignerType) error {
	return nil
}

// staticAddressStore satisfies identity.AddressStore for a single verified sender.
type staticAddressStore struct{ addr *identity.UnifiedAddress }

func (s staticAddressStore) GetByNpub(ctx context.Context, npub string) (*identity.UnifiedAddress, error) {
	if npub == s.addr.Npub {
		return s.addr, nil
	}
	return nil, nil
}
func (s staticAddressStore) GetByEmail(ctx context.Context, email string) (*identity.UnifiedAddress, error) {
	return s.addr, nil
}
func (s staticAddressStore) Create(ctx context.Context, addr *identity.UnifiedAddress) error {
	return nil
}
func (s staticAddressStore) Update(ctx context.Context, addr *identity.UnifiedAddress) error {
	return nil
}
func (s staticAddressStore) LocalPartExists(ctx context.Context, localPart string) (bool, error) {
	return true, nil
}

type nopResolver struct{}

func (nopResolver) ResolvePubkey(ctx context.Context, email string) (string, error) { return "", nil }

func TestE2EServerSideEncryptionRoundTrip(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("set DATABASE_URL to run the e2e encryption test")
	}
	logger := zap.NewNop()
	ctx := context.Background()

	db, err := storage.NewPostgres(dbURL, logger)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	defer db.Close()

	// Sender keypair (stands in for the user's bunker key).
	sk := nostr.GeneratePrivateKey()
	pub, err := nostr.GetPublicKey(sk)
	if err != nil {
		t.Fatalf("derive pubkey: %v", err)
	}

	// Real NIP-44 signer + encryption service.
	encSvc := encryption.NewEncryptionService(localSignerStore{&localNIP44Signer{sk: sk, pubkey: pub}}, logger)

	// Identity service with a verified address for the sender.
	idSvc := identity.NewService(
		staticAddressStore{&identity.UnifiedAddress{Npub: pub, LocalPart: "alice", Email: "alice@cloistr.xyz", Verified: true}},
		nopResolver{},
		logger,
	)

	svc := NewService(idSvc, nil, encSvc, db, logger)

	// Ensure a mailbox exists for the sender's pubkey (emails.mailbox_pubkey FK).
	if _, err := db.EnsureMailbox(ctx, pub); err != nil {
		t.Fatalf("ensure mailbox: %v", err)
	}

	const plaintext = "the eagle lands at midnight \U0001F985"

	// 1. Real at-rest encryption via the actual Service method.
	storedBody, mode := svc.bodyAtRest(ctx, &SendRequest{
		EncryptionMode: encryption.ModeServerSide,
		SenderNpub:     pub,
		Body:           plaintext,
	})
	if mode != "server" {
		t.Fatalf("mode = %q, want server", mode)
	}
	if storedBody == "" || storedBody == plaintext {
		t.Fatalf("body not encrypted at rest: %q", storedBody)
	}

	// 2. Persist via the real storage layer.
	em := &storage.Email{
		MailboxPubkey:  pub,
		FromAddress:    "alice@cloistr.xyz",
		ToAddress:      "bob@example.com",
		Subject:        "ops",
		Body:           storedBody,
		IsEncrypted:    true,
		EncryptionMode: &mode,
		SenderNpub:     &pub,
		Direction:      "sent",
		Folder:         "Sent",
		Status:         "active",
	}
	if err := db.CreateEmail(ctx, em); err != nil {
		t.Fatalf("create email: %v", err)
	}

	// 3. SOURCE CHECK: the row in the database holds ciphertext, not plaintext.
	raw, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer raw.Close()
	var dbBody, dbMode string
	var dbEncrypted bool
	if err := raw.QueryRowContext(ctx,
		"SELECT body, encryption_mode, is_encrypted FROM emails WHERE id = $1", em.ID,
	).Scan(&dbBody, &dbMode, &dbEncrypted); err != nil {
		t.Fatalf("raw select: %v", err)
	}
	if dbBody == plaintext {
		t.Fatalf("PLAINTEXT LEAK: body stored in clear")
	}
	if dbBody != storedBody {
		t.Fatalf("stored body mismatch")
	}
	if dbMode != "server" || !dbEncrypted {
		t.Fatalf("stored mode=%q is_encrypted=%v, want server/true", dbMode, dbEncrypted)
	}

	// 4. DESTINATION CHECK: read back through the real Service and decrypt.
	got, err := svc.GetEmail(ctx, pub, em.ID)
	if err != nil {
		t.Fatalf("get email: %v", err)
	}
	if got.RequiresClientDecryption {
		t.Fatalf("expected server-side decryption, got RequiresClientDecryption")
	}
	if got.Body != plaintext {
		t.Fatalf("round-trip mismatch:\n got:  %q\n want: %q", got.Body, plaintext)
	}

	t.Logf("OK: plaintext=%q stored as ciphertext (%d bytes), mode=%q, decrypted back equal", plaintext, len(dbBody), dbMode)
}

// TestE2EListEmailsUnprovisionedUser guards the graceful-degradation fix:
// an authenticated npub with no users row (not yet provisioned by cloistr-me /
// the onboarding flow) must get an empty inbox, NOT a 500. Regression test for
// ListEmails returning `user not found` as an error.
func TestE2EListEmailsUnprovisionedUser(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("set DATABASE_URL to run the e2e list test")
	}
	logger := zap.NewNop()
	ctx := context.Background()

	db, err := storage.NewPostgres(dbURL, logger)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	defer db.Close()

	// ListEmails only touches s.db; the other deps are irrelevant here.
	svc := NewService(nil, nil, nil, db, logger)

	// A fresh, never-provisioned pubkey: no users row exists for it.
	sk := nostr.GeneratePrivateKey()
	pub, err := nostr.GetPublicKey(sk)
	if err != nil {
		t.Fatalf("derive pubkey: %v", err)
	}

	emails, total, err := svc.ListEmails(ctx, pub, &storage.EmailFilter{Direction: "received"}, storage.ListOptions{Limit: 25})
	if err != nil {
		t.Fatalf("ListEmails for unprovisioned user should not error, got: %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}
	if len(emails) != 0 {
		t.Fatalf("len(emails) = %d, want 0", len(emails))
	}
}
