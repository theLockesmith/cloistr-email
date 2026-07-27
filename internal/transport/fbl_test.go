package transport

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/zap"
)

// realisticARF is an RFC 5965 feedback report shaped the way AOL and Microsoft
// actually emit them.
const realisticARF = `From: staff@hotmail.com
To: <abuse@cloistr.xyz>
Subject: FW: Spam Complaint
Content-Type: multipart/report; report-type=feedback-report; boundary="ARF"

--ARF
Content-Type: text/plain; charset="US-ASCII"

This is an email abuse report for an email message received on 2026-07-26.

--ARF
Content-Type: message/feedback-report

Feedback-Type: abuse
User-Agent: SomeGenerator/1.0
Version: 1
Original-Mail-From: alice@cloistr.xyz
Original-Rcpt-To: someone@hotmail.com
Reporting-MTA: dns; mx.hotmail.com
Source-IP: 203.0.113.10

--ARF
Content-Type: message/rfc822

From: alice@cloistr.xyz
To: someone@hotmail.com
Message-ID: <reported-xyz789@cloistr.xyz>
Subject: buy things

body
--ARF--
`

func TestIsComplaintDetectsARF(t *testing.T) {
	h := NewFBLHandler(nil, zap.NewNop())

	if !h.IsComplaint([]byte(realisticARF)) {
		t.Error("did not recognise an ARF feedback report")
	}
	if h.IsComplaint([]byte(realisticDSN)) {
		t.Error("misidentified a bounce as a spam complaint")
	}
}

// An ARF report is also a multipart/report. If a provider omits the report-type
// parameter, the body must still identify it — otherwise the strongest abuse
// signal gets misfiled as the weaker one.
func TestIsComplaintDetectsARFWithoutReportTypeParam(t *testing.T) {
	const arf = `From: staff@example.com
To: <abuse@cloistr.xyz>
Subject: Spam Complaint
Content-Type: multipart/report; boundary="ARF"

--ARF
Content-Type: message/feedback-report

Feedback-Type: abuse
Original-Rcpt-To: someone@example.com

--ARF--
`

	if !NewFBLHandler(nil, zap.NewNop()).IsComplaint([]byte(arf)) {
		t.Error("did not recognise an ARF report lacking report-type=feedback-report")
	}
}

func TestParseComplaintExtractsARFFields(t *testing.T) {
	h := NewFBLHandler(nil, zap.NewNop())

	info, err := h.parseComplaint([]byte(realisticARF))
	if err != nil {
		t.Fatalf("parseComplaint: %v", err)
	}

	if info.FeedbackType != "abuse" {
		t.Errorf("FeedbackType = %q, want %q", info.FeedbackType, "abuse")
	}
	if want := "someone@hotmail.com"; info.OriginalRecipient != want {
		t.Errorf("OriginalRecipient = %q, want %q", info.OriginalRecipient, want)
	}
	if want := "mx.hotmail.com"; info.ReportingMTA != want {
		t.Errorf("ReportingMTA = %q, want %q", info.ReportingMTA, want)
	}
	// The reported message's Message-ID, not the report's own.
	if want := "<reported-xyz789@cloistr.xyz>"; info.OriginalMessageID != want {
		t.Errorf("OriginalMessageID = %q, want %q", info.OriginalMessageID, want)
	}
}

// A report with no recognisable Feedback-Type is still worth recording.
func TestParseComplaintDefaultsFeedbackType(t *testing.T) {
	const arf = `From: staff@example.com
To: <abuse@cloistr.xyz>
Content-Type: multipart/report; report-type=feedback-report; boundary="ARF"

--ARF
Content-Type: message/feedback-report

Original-Rcpt-To: someone@example.com

--ARF--
`

	info, err := NewFBLHandler(nil, zap.NewNop()).parseComplaint([]byte(arf))
	if err != nil {
		t.Fatalf("parseComplaint: %v", err)
	}
	if info.FeedbackType != "other" {
		t.Errorf("FeedbackType = %q, want %q", info.FeedbackType, "other")
	}
}

func TestProcessComplaintAttributesAndStores(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const pubkey = "deadbeef"
	mock.ExpectExec("INSERT INTO email_complaints").
		WithArgs("someone@hotmail.com", "<reported-xyz789@cloistr.xyz>", "abuse",
			"mx.hotmail.com", sqlmock.AnyArg(), pubkey).
		WillReturnResult(sqlmock.NewResult(1, 1))

	h := NewFBLHandler(db, zap.NewNop(), WithFBLSenderResolver(
		func(context.Context, string, string) (string, error) { return pubkey, nil }))

	if err := h.ProcessComplaint(t.Context(), []byte(realisticARF)); err != nil {
		t.Fatalf("ProcessComplaint: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// Providers routinely redact the recipient, so many reports cannot be tied to an
// account. Those must still be stored — with NULL — for operator review.
func TestProcessComplaintStoresUnattributedAsNull(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO email_complaints").
		WithArgs("someone@hotmail.com", "<reported-xyz789@cloistr.xyz>", "abuse",
			"mx.hotmail.com", sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))

	h := NewFBLHandler(db, zap.NewNop(), WithFBLSenderResolver(
		func(context.Context, string, string) (string, error) { return "", nil }))

	if err := h.ProcessComplaint(t.Context(), []byte(realisticARF)); err != nil {
		t.Fatalf("ProcessComplaint: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// A deployment without migration 010 simply does not collect complaints; that
// must not make inbound mail fail.
func TestProcessComplaintToleratesMissingTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO email_complaints").
		WillReturnError(errMissingRelation{})

	h := NewFBLHandler(db, zap.NewNop(), WithFBLSenderResolver(
		func(context.Context, string, string) (string, error) { return "", nil }))

	if err := h.ProcessComplaint(t.Context(), []byte(realisticARF)); err != nil {
		t.Errorf("ProcessComplaint: %v", err)
	}
}

type errMissingRelation struct{}

func (errMissingRelation) Error() string { return `relation "email_complaints" does not exist` }
