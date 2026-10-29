package email

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/blossom"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Filenames are user content. Upload-failure logs carry the attachment row id
// and size instead.
const secretFilename = "divorce-papers-final.pdf"

func flattenLogs(logs *observer.ObservedLogs) string {
	var b strings.Builder
	for _, e := range logs.All() {
		b.WriteString(e.Message)
		for k, v := range e.ContextMap() {
			b.WriteString(" " + k + "=")
			if s, ok := v.(string); ok {
				b.WriteString(s)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestOutboundUploadFailureDoesNotLogFilename(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	svc := NewService(nil, nil, nil, nil, zap.New(core)).WithBlossom([]blossom.Server{{URL: "http://127.0.0.1:1"}}, 1)
	// No sealer: the unencrypted-mode attachment is refused, which is logged.
	svc.uploadAttachments(context.Background(), "pk", encryption.ModeNone, "email-1", []AttachmentInput{{Filename: secretFilename, Data: []byte("abc")}})
	got := flattenLogs(logs)
	if got == "" {
		t.Fatal("expected a log line for the skipped attachment")
	}
	if strings.Contains(got, secretFilename) || strings.Contains(got, "divorce") {
		t.Fatalf("log carries the filename:\n%s", got)
	}
	if !strings.Contains(got, "attachment_id=") {
		t.Fatalf("log does not identify the attachment by id:\n%s", got)
	}
}

type failingUploader struct{}

func (failingUploader) UploadFor(context.Context, string, []byte) (StoredBlob, error) {
	return StoredBlob{}, errors.New("blossom down")
}

func TestE2EInboundUploadFailureDoesNotLogFilename(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("set DATABASE_URL to run")
	}
	ctx := context.Background()
	db, err := storage.NewPostgres(dbURL, zap.NewNop())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	me, _ := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	if _, err := db.EnsureMailbox(ctx, me); err != nil {
		t.Fatalf("mailbox: %v", err)
	}
	em := &storage.Email{MailboxPubkey: me, FromAddress: "x@example.org", ToAddress: "me@cloistr.xyz", Subject: "s", Body: "b", Direction: "received"}
	if err := db.CreateEmail(ctx, em); err != nil {
		t.Fatalf("email: %v", err)
	}

	core, logs := observer.New(zap.DebugLevel)
	proc := NewInboundProcessor(db, nil, zap.New(core))
	proc.SetAttachmentUploader(failingUploader{})
	proc.storeAttachments(ctx, em.ID, me, []ParsedAttachment{{Filename: secretFilename, ContentType: "application/pdf", Data: []byte("abc")}})

	got := flattenLogs(logs)
	if strings.Contains(got, secretFilename) || strings.Contains(got, "divorce") {
		t.Fatalf("log carries the filename:\n%s", got)
	}
	atts, _ := db.GetAttachmentsByEmail(ctx, em.ID)
	if len(atts) != 1 || !strings.Contains(got, "attachment_id="+atts[0].ID) {
		t.Fatalf("log does not name the stored row id (%d rows):\n%s", len(atts), got)
	}
}
