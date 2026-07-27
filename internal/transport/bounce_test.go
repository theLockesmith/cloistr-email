package transport

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/zap"
)

// realisticDSN is a multipart/report bounce shaped the way Postfix and Google
// actually emit them: the interesting fields live in the delivery-status part
// and the returned original message, never in the top-level headers.
const realisticDSN = `From: MAILER-DAEMON@mx.example.net
To: <bounces@cloistr.xyz>
Subject: Undelivered Mail Returned to Sender
Content-Type: multipart/report; report-type=delivery-status; boundary="XYZ"

--XYZ
Content-Type: text/plain; charset=us-ascii

This is the mail system at host mx.example.net.

--XYZ
Content-Type: message/delivery-status

Reporting-MTA: dns; mx.example.net

Final-Recipient: rfc822; nobody@example.com
Original-Recipient: rfc822; nobody@example.com
Action: failed
Status: 5.1.1
Diagnostic-Code: smtp; 550 5.1.1 <nobody@example.com>: Recipient address rejected: User unknown

--XYZ
Content-Type: message/rfc822

From: alice@cloistr.xyz
To: nobody@example.com
Message-ID: <original-abc123@cloistr.xyz>
Subject: hello

body text
--XYZ--
`

func TestParseBounceExtractsDSNPartFields(t *testing.T) {
	h := NewBounceHandler(nil, zap.NewNop())

	info, err := h.parseBounce([]byte(realisticDSN))
	if err != nil {
		t.Fatalf("parseBounce: %v", err)
	}

	if want := "<original-abc123@cloistr.xyz>"; info.OriginalMessageID != want {
		t.Errorf("OriginalMessageID = %q, want %q", info.OriginalMessageID, want)
	}
	if want := "nobody@example.com"; info.OriginalRecipient != want {
		t.Errorf("OriginalRecipient = %q, want %q", info.OriginalRecipient, want)
	}
	if want := "550 5.1.1"; info.DiagnosticCode != want {
		t.Errorf("DiagnosticCode = %q, want %q", info.DiagnosticCode, want)
	}
	if info.Type != BounceTypeHard {
		t.Errorf("Type = %q, want %q", info.Type, BounceTypeHard)
	}
}

// A bounce whose Message-ID is only recoverable from the returned copy must
// still not be mistaken for the recipient address — the old extractor read
// X-Failed-Recipients as a Message-ID, which poisoned attribution lookups.
func TestParseBounceDoesNotUseFailedRecipientsAsMessageID(t *testing.T) {
	const dsn = `From: MAILER-DAEMON@mx.example.net
To: <bounces@cloistr.xyz>
Subject: Delivery Status Notification (Failure)
X-Failed-Recipients: nobody@example.com
Content-Type: text/plain

550 5.1.1 user unknown
`

	h := NewBounceHandler(nil, zap.NewNop())

	info, err := h.parseBounce([]byte(dsn))
	if err != nil {
		t.Fatalf("parseBounce: %v", err)
	}

	if info.OriginalMessageID == "nobody@example.com" {
		t.Error("OriginalMessageID picked up the failed-recipient address")
	}
	if want := "nobody@example.com"; info.OriginalRecipient != want {
		t.Errorf("OriginalRecipient = %q, want %q", info.OriginalRecipient, want)
	}
}

func TestAttributeSenderUsesResolver(t *testing.T) {
	const pubkey = "aa" + "00000000000000000000000000000000000000000000000000000000000000"

	var gotMessageID, gotRecipient string
	h := NewBounceHandler(nil, zap.NewNop(), WithSenderResolver(
		func(_ context.Context, messageID, recipient string) (string, error) {
			gotMessageID, gotRecipient = messageID, recipient
			return pubkey, nil
		}))

	info := &BounceInfo{
		OriginalMessageID: "<original-abc123@cloistr.xyz>",
		OriginalRecipient: "nobody@example.com",
	}
	h.attributeSender(context.Background(), info)

	if info.SenderPubkey != pubkey {
		t.Errorf("SenderPubkey = %q, want %q", info.SenderPubkey, pubkey)
	}
	if gotMessageID != info.OriginalMessageID || gotRecipient != info.OriginalRecipient {
		t.Errorf("resolver got (%q, %q), want (%q, %q)",
			gotMessageID, gotRecipient, info.OriginalMessageID, info.OriginalRecipient)
	}
}

// A resolver failure must not lose the bounce: it is still recorded, just
// unattributed.
func TestAttributeSenderToleratesResolverFailure(t *testing.T) {
	h := NewBounceHandler(nil, zap.NewNop(), WithSenderResolver(
		func(context.Context, string, string) (string, error) {
			return "", sql.ErrConnDone
		}))

	info := &BounceInfo{OriginalRecipient: "nobody@example.com"}
	h.attributeSender(context.Background(), info)

	if info.SenderPubkey != "" {
		t.Errorf("SenderPubkey = %q, want empty", info.SenderPubkey)
	}
}

// An already-known sender (the outbound-failure path) must not be overwritten
// by a lookup.
func TestAttributeSenderKeepsKnownPubkey(t *testing.T) {
	h := NewBounceHandler(nil, zap.NewNop(), WithSenderResolver(
		func(context.Context, string, string) (string, error) {
			t.Error("resolver called even though the sender was already known")
			return "other", nil
		}))

	info := &BounceInfo{SenderPubkey: "known"}
	h.attributeSender(context.Background(), info)

	if info.SenderPubkey != "known" {
		t.Errorf("SenderPubkey = %q, want %q", info.SenderPubkey, "known")
	}
}

func TestQueueSenderResolverByMessageID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const pubkey = "deadbeef"
	mock.ExpectQuery("FROM outbound_queue").
		WithArgs("<original-abc123@cloistr.xyz>").
		WillReturnRows(sqlmock.NewRows([]string{"sender_pubkey"}).AddRow(pubkey))

	got, err := QueueSenderResolver(db)(context.Background(),
		"<original-abc123@cloistr.xyz>", "nobody@example.com")
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if got != pubkey {
		t.Errorf("pubkey = %q, want %q", got, pubkey)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// When the DSN carries no usable Message-ID, attribution falls back to the most
// recent message sent to that recipient.
func TestQueueSenderResolverFallsBackToRecipient(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const pubkey = "cafebabe"
	mock.ExpectQuery(regexp.QuoteMeta("recipients @> to_jsonb")).
		WithArgs("nobody@example.com", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"sender_pubkey"}).AddRow(pubkey))

	got, err := QueueSenderResolver(db)(context.Background(), "", "nobody@example.com")
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if got != pubkey {
		t.Errorf("pubkey = %q, want %q", got, pubkey)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// A message-ID miss must fall through to the recipient lookup rather than
// giving up, since remote MTAs routinely rewrite or omit the Message-ID.
func TestQueueSenderResolverFallsThroughOnMessageIDMiss(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const pubkey = "cafebabe"
	mock.ExpectQuery("message_id = ").
		WithArgs("<unknown@elsewhere>").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta("recipients @> to_jsonb")).
		WithArgs("nobody@example.com", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"sender_pubkey"}).AddRow(pubkey))

	got, err := QueueSenderResolver(db)(context.Background(),
		"<unknown@elsewhere>", "nobody@example.com")
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if got != pubkey {
		t.Errorf("pubkey = %q, want %q", got, pubkey)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestStoreBounceWritesSenderPubkey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const pubkey = "deadbeef"
	mock.ExpectExec("INSERT INTO email_bounces").
		WithArgs("nobody@example.com", "<mid@cloistr.xyz>", BounceTypeHard,
			"user unknown", "550 5.1.1", sqlmock.AnyArg(), pubkey).
		WillReturnResult(sqlmock.NewResult(1, 1))

	h := NewBounceHandler(db, zap.NewNop())
	err = h.storeBounce(context.Background(), &BounceInfo{
		Type:              BounceTypeHard,
		OriginalRecipient: "nobody@example.com",
		OriginalMessageID: "<mid@cloistr.xyz>",
		Reason:            "user unknown",
		DiagnosticCode:    "550 5.1.1",
		ReceivedAt:        time.Now(),
		SenderPubkey:      pubkey,
	})
	if err != nil {
		t.Fatalf("storeBounce: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// Unattributed bounces store NULL, not "", so the partial index stays small and
// rate queries cannot pick up phantom rows.
func TestStoreBounceWritesNullForUnattributedSender(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO email_bounces").
		WithArgs("nobody@example.com", "", BounceTypeUnknown, "", "",
			sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))

	h := NewBounceHandler(db, zap.NewNop())
	err = h.storeBounce(context.Background(), &BounceInfo{
		Type:              BounceTypeUnknown,
		OriginalRecipient: "nobody@example.com",
		ReceivedAt:        time.Now(),
	})
	if err != nil {
		t.Fatalf("storeBounce: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestRecordOutboundFailureAttributesSender(t *testing.T) {
	const pubkey = "deadbeef"

	var gotHard *BounceInfo
	h := NewBounceHandler(nil, zap.NewNop(), WithHardBounceCallback(
		func(_ context.Context, b *BounceInfo) error {
			gotHard = b
			return nil
		}))

	err := h.RecordOutboundFailure(context.Background(), "<mid@cloistr.xyz>", pubkey,
		[]string{"nobody@example.com"}, errUserUnknown{})
	if err != nil {
		t.Fatalf("RecordOutboundFailure: %v", err)
	}

	if gotHard == nil {
		t.Fatal("hard bounce callback was not called")
	}
	if gotHard.SenderPubkey != pubkey {
		t.Errorf("SenderPubkey = %q, want %q", gotHard.SenderPubkey, pubkey)
	}
}

type errUserUnknown struct{}

func (errUserUnknown) Error() string { return "550 5.1.1 user unknown" }

func TestSenderBounceCounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const pubkey = "deadbeef"
	since := time.Now().Add(-24 * time.Hour)

	mock.ExpectQuery("FROM email_bounces").
		WithArgs(pubkey, since, BounceTypeHard, BounceTypeSoft).
		WillReturnRows(sqlmock.NewRows([]string{"hard", "soft", "total"}).AddRow(7, 2, 10))

	h := NewBounceHandler(db, zap.NewNop())
	counts, err := h.SenderBounceCounts(context.Background(), pubkey, since)
	if err != nil {
		t.Fatalf("SenderBounceCounts: %v", err)
	}

	if counts.Hard != 7 || counts.Soft != 2 || counts.Total != 10 {
		t.Errorf("counts = %+v, want {Hard:7 Soft:2 Total:10}", counts)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// An empty pubkey must never turn into a query — an unattributed bounce has no
// account to hold accountable.
func TestSenderBounceCountsIgnoresEmptyPubkey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	h := NewBounceHandler(db, zap.NewNop())
	counts, err := h.SenderBounceCounts(context.Background(), "", time.Now())
	if err != nil {
		t.Fatalf("SenderBounceCounts: %v", err)
	}
	if counts.Total != 0 {
		t.Errorf("Total = %d, want 0", counts.Total)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
