package email

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"
)

// TestE2EServerDrafts pins server-side drafts: a draft is created once and
// then overwritten in place, only its owner can overwrite it, it shows in the
// Drafts list, an encrypted draft must arrive as a client envelope the owner
// can open (the server never holds its plaintext), and server-mode drafts are
// refused because they would need the server to encrypt.
//
//	Run with: DATABASE_URL='postgres://...?sslmode=disable&search_path=email,public' \
//	  go test ./internal/email/ -run ServerDrafts -v
func TestE2EServerDrafts(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("set DATABASE_URL to run the e2e drafts test")
	}
	logger := zap.NewNop()
	ctx := context.Background()

	db, err := storage.NewPostgres(dbURL, logger)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := NewService(nil, nil, nil, db, logger)

	// The envelope fixture's sender is the draft owner, so the owner holds a
	// wrapped key for it.
	envBytes, owner, _ := withKeyFor(t, bodyFixture)
	envelope := string(envBytes)
	other, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	for _, pk := range []string{owner, other} {
		if _, err := db.EnsureMailbox(ctx, pk); err != nil {
			t.Fatalf("ensure mailbox: %v", err)
		}
	}

	// The fixture's owner key is fixed, so remove rows left by earlier runs.
	if _, err := db.DB().ExecContext(ctx, `DELETE FROM emails WHERE mailbox_pubkey = $1`, owner); err != nil {
		t.Fatalf("reset owner mailbox: %v", err)
	}

	draftTotal := func(pk string) int {
		t.Helper()
		_, n, err := svc.ListEmails(ctx, pk, &storage.EmailFilter{Direction: "draft"}, storage.ListOptions{Limit: 50})
		if err != nil {
			t.Fatalf("list drafts: %v", err)
		}
		return n
	}

	// Create.
	d, err := svc.SaveDraft(ctx, owner, &DraftInput{
		To:      "bob@example.org",
		CC:      []string{"carol@example.org"},
		Subject: "first",
		Body:    "hello",
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if d.ID == "" {
		t.Fatal("created draft has no id")
	}
	if n := draftTotal(owner); n != 1 {
		t.Fatalf("drafts after create = %d, want 1", n)
	}

	// Overwrite in place: same id, still one draft, new content readable.
	d2, err := svc.SaveDraft(ctx, owner, &DraftInput{ID: d.ID, To: "bob@example.org", Subject: "second", Body: "hello again"})
	if err != nil {
		t.Fatalf("update draft: %v", err)
	}
	if d2.ID != d.ID {
		t.Fatalf("update changed id %s -> %s", d.ID, d2.ID)
	}
	if n := draftTotal(owner); n != 1 {
		t.Fatalf("drafts after update = %d, want 1", n)
	}
	got, err := db.GetEmail(ctx, d.ID)
	if err != nil || got == nil {
		t.Fatalf("get draft: %v", err)
	}
	if got.Subject != "second" || got.Body != "hello again" || got.Direction != "draft" {
		t.Fatalf("stored draft = %q/%q/%q, want second/hello again/draft", got.Subject, got.Body, got.Direction)
	}
	if got.CC != nil && *got.CC != "" {
		t.Fatalf("cc after update = %q, want cleared", *got.CC)
	}

	// Another user cannot overwrite it, and cannot learn it exists.
	if _, err := svc.SaveDraft(ctx, other, &DraftInput{ID: d.ID, Subject: "hijack", Body: "x"}); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("other user's update: err = %v, want ErrDraftNotFound", err)
	}
	// A sent message is not a draft and cannot be overwritten through this path.
	sent := &storage.Email{MailboxPubkey: owner, FromAddress: "me@cloistr.xyz", ToAddress: "bob@example.org", Subject: "s", Body: "b", Direction: "sent"}
	if err := db.CreateEmail(ctx, sent); err != nil {
		t.Fatalf("create sent: %v", err)
	}
	if _, err := svc.SaveDraft(ctx, owner, &DraftInput{ID: sent.ID, Subject: "rewrite", Body: "x"}); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("overwriting a sent message: err = %v, want ErrDraftNotFound", err)
	}

	// Encrypted drafts: plaintext is refused, an envelope the owner can open is
	// stored under the placeholder subject.
	if _, err := svc.SaveDraft(ctx, owner, &DraftInput{Subject: "secret", Body: "plain", EncryptionMode: encryption.ModeClientSide}); !errors.Is(err, ErrClientBodyNotEncrypted) {
		t.Fatalf("plaintext client draft: err = %v, want ErrClientBodyNotEncrypted", err)
	}
	if _, err := svc.SaveDraft(ctx, other, &DraftInput{Body: envelope, EncryptionMode: encryption.ModeClientSide}); !errors.Is(err, ErrDraftKeyNotOwner) {
		t.Fatalf("envelope the owner holds no key for: err = %v, want ErrDraftKeyNotOwner", err)
	}
	enc, err := svc.SaveDraft(ctx, owner, &DraftInput{To: "bob@example.org", Subject: "secret subject", Body: envelope, EncryptionMode: encryption.ModeClientSide})
	if err != nil {
		t.Fatalf("client draft: %v", err)
	}
	gotEnc, err := db.GetEmail(ctx, enc.ID)
	if err != nil || gotEnc == nil {
		t.Fatalf("get client draft: %v", err)
	}
	if gotEnc.Subject != EncryptedSubjectPlaceholder || !gotEnc.IsEncrypted || gotEnc.Body != envelope {
		t.Fatalf("client draft stored subject=%q encrypted=%v, want placeholder and envelope", gotEnc.Subject, gotEnc.IsEncrypted)
	}

	if _, err := svc.SaveDraft(ctx, owner, &DraftInput{Subject: "s", Body: "b", EncryptionMode: encryption.ModeServerSide}); !errors.Is(err, ErrDraftServerMode) {
		t.Fatalf("server-mode draft: err = %v, want ErrDraftServerMode", err)
	}
	if _, err := svc.SaveDraft(ctx, owner, &DraftInput{Body: strings.Repeat("x", MaxDraftBodyBytes+1)}); !errors.Is(err, ErrDraftTooLarge) {
		t.Fatalf("oversized draft: err = %v, want ErrDraftTooLarge", err)
	}

	// Deleting a draft (after it is sent, or discarded) uses the normal delete.
	if err := svc.DeleteEmail(ctx, owner, d.ID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if n := draftTotal(owner); n != 1 {
		t.Fatalf("drafts after delete = %d, want 1 (the encrypted one)", n)
	}
	if n := draftTotal(other); n != 0 {
		t.Fatalf("other user's drafts = %d, want 0", n)
	}
}
