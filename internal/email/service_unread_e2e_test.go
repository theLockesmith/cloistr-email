package email

import (
	"context"
	"os"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"
)

// TestE2EUnreadInboxCount pins the number behind the Inbox unread badge: it
// must equal what the Inbox list shows as unread (folder INBOX, not deleted,
// read_at NULL) for this mailbox only. Read mail, mail in other folders,
// deleted mail and another user's mail must not count.
//
//	Run with: DATABASE_URL='postgres://...?sslmode=disable&search_path=email,public' \
//	  go test ./internal/email/ -run UnreadInboxCount -v
//
// (search_path mirrors production's role default; the migrator only sets it
// for its own transaction.)
func TestE2EUnreadInboxCount(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("set DATABASE_URL to run the e2e unread-count test")
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

	me, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	other, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	for _, pk := range []string{me, other} {
		if _, err := db.EnsureMailbox(ctx, pk); err != nil {
			t.Fatalf("ensure mailbox: %v", err)
		}
	}

	mk := func(mailbox, direction, folder string) *storage.Email {
		e := &storage.Email{
			MailboxPubkey: mailbox,
			FromAddress:   "sender@example.org",
			ToAddress:     "me@cloistr.xyz",
			Subject:       "unread-count",
			Body:          "b",
			Direction:     direction,
			Folder:        folder,
		}
		if err := db.CreateEmail(ctx, e); err != nil {
			t.Fatalf("create email: %v", err)
		}
		return e
	}

	n, err := svc.UnreadInboxCount(ctx, me)
	if err != nil {
		t.Fatalf("UnreadInboxCount on empty mailbox: %v", err)
	}
	if n != 0 {
		t.Fatalf("empty mailbox: unread = %d, want 0", n)
	}

	mk(me, "received", "INBOX")
	mk(me, "received", "INBOX")
	read := mk(me, "received", "INBOX")
	deleted := mk(me, "received", "INBOX")
	mk(me, "received", "Archive")
	mk(me, "sent", "Sent")
	mk(other, "received", "INBOX")

	if err := svc.MarkEmailRead(ctx, me, read.ID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if err := db.DeleteEmail(ctx, deleted.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	n, err = svc.UnreadInboxCount(ctx, me)
	if err != nil {
		t.Fatalf("UnreadInboxCount: %v", err)
	}
	if n != 2 {
		t.Fatalf("unread = %d, want 2 (two unread INBOX messages)", n)
	}

	// The badge must agree with the Inbox list filtered to unread.
	unread := true
	_, listed, err := svc.ListEmails(ctx, me, &storage.EmailFilter{Folder: "INBOX", Unread: &unread}, storage.ListOptions{Limit: 50})
	if err != nil {
		t.Fatalf("ListEmails: %v", err)
	}
	if listed != n {
		t.Fatalf("badge %d disagrees with Inbox unread list total %d", n, listed)
	}
}
