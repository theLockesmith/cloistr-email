package storage

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// TestCreateEmailStoresEveryLocalRecipientCopyDespiteSharedMessageID
// (fixed by configs/migrations/013_message_id_unique_per_mailbox.sql)
// reproduces MAIL-26 at the level the bug actually lives at: Postgres.
//
// A sends "To: B, Cc: A" (A CC's themselves). That legitimately produces
// THREE rows sharing one Message-ID: A's own Sent copy, B's Received (inbox)
// copy, and A's own Received (inbox) copy from the Cc. configs/schema.sql
// declares `message_id VARCHAR(255) UNIQUE` on the `emails` table -- a
// TABLE-WIDE unique constraint, not one scoped to (mailbox_pubkey,
// message_id) -- so only the FIRST of those three inserts can keep the real
// Message-ID; every later one is a Postgres unique_violation. Before the fix
// in CreateEmail (internal/storage/postgres.go), that violation propagated
// straight up: internal/email/inbound.go's HandleMessage loop logs the error
// for that recipient and moves on to the next one ("Continue with other
// recipients"), so the copy is silently never stored. That is exactly the
// live symptom: B gets the mail, A's own Cc copy never appears in A's inbox.
//
// Run with: TEST_PG_ADMIN_URL='postgres://postgres:test@127.0.0.1:5432/postgres?sslmode=disable' \
//
//	go test ./internal/storage/ -run TestCreateEmailStoresEveryLocalRecipientCopyDespiteSharedMessageID -v
func TestCreateEmailStoresEveryLocalRecipientCopyDespiteSharedMessageID(t *testing.T) {
	sqlDB := freshDB(t)
	m := newTestMigrator(t, sqlDB)
	ctx := context.Background()
	if err := m.Run(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The migrator only sets search_path for the transaction it runs in
	// (internal/storage/migrate.go: `SET LOCAL search_path = email, public`).
	// Production's role has a standing `search_path = email, public` default
	// (see internal/storage/testdata/prod_email_schema_20261006.sql header),
	// which a plain `postgres` superuser on a throwaway test database does
	// not. Pin the pool to one physical connection and set the search_path on
	// it directly so every query below (through NewPostgresForTesting, which
	// issues unqualified table names like the production code does) resolves
	// against the `email` schema the same way it does in production.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if _, err := sqlDB.Exec(`SET search_path TO email, public`); err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	db := NewPostgresForTesting(sqlDB, zap.NewNop())

	const (
		senderPubkey    = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
		recipientPubkey = "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"
		senderAddr      = "clever-thicket-6621@cloistr.xyz"
		recipientAddr   = "b@cloistr.xyz"
		messageID       = "msg-mail26@cloistr.xyz"
	)

	if _, err := db.EnsureMailbox(ctx, senderPubkey); err != nil {
		t.Fatalf("ensure sender mailbox: %v", err)
	}
	if _, err := db.EnsureMailbox(ctx, recipientPubkey); err != nil {
		t.Fatalf("ensure recipient mailbox: %v", err)
	}

	msgID := messageID

	// 1. The sender's own Sent copy -- stored exactly as
	// internal/email/service.go Send() step 6 does, first.
	sent := &Email{
		MailboxPubkey: senderPubkey,
		MessageID:     &msgID,
		FromAddress:   senderAddr,
		ToAddress:     recipientAddr,
		Direction:     "sent",
		Folder:        "sent",
	}
	if err := db.CreateEmail(ctx, sent); err != nil {
		t.Fatalf("create sent copy: %v", err)
	}

	// 2. Recipient B's inbox copy, as internal/email/inbound.go
	// storeForRecipient stores it for the first RCPT TO.
	msgID2 := messageID
	toB := &Email{
		MailboxPubkey: recipientPubkey,
		MessageID:     &msgID2,
		FromAddress:   senderAddr,
		ToAddress:     recipientAddr,
		Direction:     "received",
		Folder:        "INBOX",
	}
	if err := db.CreateEmail(ctx, toB); err != nil {
		t.Fatalf("create recipient copy: %v", err)
	}

	// 3. The sender's OWN inbox copy from Cc'ing themselves -- same
	// Message-ID, same storeForRecipient path, landing in the SENDER's own
	// mailbox this time. This is the row that MAIL-26 says never shows up.
	msgID3 := messageID
	ccSelf := &Email{
		MailboxPubkey: senderPubkey,
		MessageID:     &msgID3,
		FromAddress:   senderAddr,
		ToAddress:     senderAddr,
		Direction:     "received",
		Folder:        "INBOX",
	}
	if err := db.CreateEmail(ctx, ccSelf); err != nil {
		t.Fatalf("Cc-to-self copy was not stored (MAIL-26 reproduced): %v", err)
	}
	if ccSelf.ID == "" {
		t.Fatal("Cc-to-self copy has no ID; it was not actually inserted")
	}

	// It must be a genuinely separate row from the Sent copy: same mailbox,
	// but a distinct id, distinct direction/folder.
	if ccSelf.ID == sent.ID {
		t.Fatal("Cc-to-self copy reused the Sent row's id instead of being its own row")
	}

	got, err := db.GetEmail(ctx, ccSelf.ID)
	if err != nil || got == nil {
		t.Fatalf("Cc-to-self copy cannot be read back: err=%v got=%v", err, got)
	}
	if got.MailboxPubkey != senderPubkey {
		t.Errorf("Cc-to-self copy landed in mailbox %q, want sender's own mailbox %q", got.MailboxPubkey, senderPubkey)
	}
	if got.Direction != "received" || got.Folder != "INBOX" {
		t.Errorf("Cc-to-self copy has direction=%q folder=%q, want received/INBOX", got.Direction, got.Folder)
	}

	// The sender's original Sent row must be untouched: still exactly one
	// sent row, still carrying the real Message-ID.
	gotSent, err := db.GetEmail(ctx, sent.ID)
	if err != nil || gotSent == nil {
		t.Fatalf("Sent copy cannot be read back: err=%v got=%v", err, gotSent)
	}
	if gotSent.Direction != "sent" {
		t.Errorf("Sent copy direction changed to %q", gotSent.Direction)
	}
	if gotSent.MessageID == nil || *gotSent.MessageID != messageID {
		t.Errorf("Sent copy's Message-ID should be untouched, got %v", gotSent.MessageID)
	}
	// Migration 013 scopes uniqueness to (mailbox, direction, Message-ID), so
	// every copy keeps its real Message-ID (threading needs it).
	for name, id := range map[string]string{"recipient copy": toB.ID, "Cc-to-self copy": ccSelf.ID} {
		row, err := db.GetEmail(ctx, id)
		if err != nil || row == nil || row.MessageID == nil || *row.MessageID != messageID {
			t.Errorf("%s lost its Message-ID: %+v err=%v", name, row, err)
		}
	}

	// A second delivery of the same message into the same mailbox and
	// direction is still refused.
	dupID := messageID
	dup := &Email{MailboxPubkey: recipientPubkey, MessageID: &dupID, FromAddress: senderAddr, ToAddress: recipientAddr, Direction: "received", Folder: "INBOX"}
	if err := db.CreateEmail(ctx, dup); err == nil {
		t.Fatal("a duplicate delivery into the same mailbox and direction was accepted")
	}
}
