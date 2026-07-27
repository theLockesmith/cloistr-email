package usage

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/zap"
)

type recordedCall struct {
	pubkey string
	amount int64
}

type fakeRecorder struct {
	calls []recordedCall
	err   error
}

func (f *fakeRecorder) RecordUsage(_ context.Context, pubkey, _ string, amount int64) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, recordedCall{pubkey, amount})
	return nil
}

// expectMeasurements queues the three reads a pass performs, in order.
func expectMeasurements(mock sqlmock.Sqlmock, messages, attachments, recorded *sqlmock.Rows) {
	mock.ExpectQuery("FROM emails").WillReturnRows(messages)
	mock.ExpectQuery("FROM attachments").WillReturnRows(attachments)
	mock.ExpectQuery("FROM user_quota_usage").WillReturnRows(recorded)
}

func messageRows() *sqlmock.Rows  { return sqlmock.NewRows([]string{"pubkey", "bytes"}) }
func recordedRows() *sqlmock.Rows { return sqlmock.NewRows([]string{"pubkey", "bytes"}) }

func TestReconcileRecordsDeltaNotAbsolute(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	expectMeasurements(mock,
		messageRows().AddRow("alice", 1000),
		messageRows(),
		recordedRows().AddRow("alice", 400),
	)

	rec := &fakeRecorder{}
	result, err := New(db, rec, zap.NewNop()).Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("got %d RecordUsage calls, want 1", len(rec.calls))
	}
	// 600, not 1000 — the platform API is additive, so reporting the absolute
	// measurement would double-count everything already on record.
	if rec.calls[0] != (recordedCall{"alice", 600}) {
		t.Errorf("call = %+v, want {alice 600}", rec.calls[0])
	}
	if result.Corrected != 1 || result.NetBytes != 600 {
		t.Errorf("result = %+v, want Corrected=1 NetBytes=600", result)
	}
}

func TestReconcileSkipsMailboxesAlreadyInSync(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	expectMeasurements(mock,
		messageRows().AddRow("alice", 1000),
		messageRows(),
		recordedRows().AddRow("alice", 1000),
	)

	rec := &fakeRecorder{}
	result, err := New(db, rec, zap.NewNop()).Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(rec.calls) != 0 {
		t.Errorf("got %d RecordUsage calls, want 0", len(rec.calls))
	}
	if result.Mailboxes != 1 || result.Corrected != 0 {
		t.Errorf("result = %+v, want Mailboxes=1 Corrected=0", result)
	}
}

// The leak this whole package exists to fix: a mailbox that deleted everything
// still has a stale component, and nothing measures it unless the pass walks
// recorded rows too.
func TestReconcileCorrectsEmptiedMailboxDown(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	expectMeasurements(mock,
		messageRows(),
		messageRows(),
		recordedRows().AddRow("alice", 5000),
	)

	rec := &fakeRecorder{}
	result, err := New(db, rec, zap.NewNop()).Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("got %d RecordUsage calls, want 1", len(rec.calls))
	}
	if rec.calls[0] != (recordedCall{"alice", -5000}) {
		t.Errorf("call = %+v, want {alice -5000}", rec.calls[0])
	}
	if result.NetBytes != -5000 {
		t.Errorf("NetBytes = %d, want -5000", result.NetBytes)
	}
}

func TestReconcileIncludesInlineAttachments(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	expectMeasurements(mock,
		messageRows().AddRow("alice", 1000),
		messageRows().AddRow("alice", 250),
		recordedRows(),
	)

	rec := &fakeRecorder{}
	if _, err := New(db, rec, zap.NewNop()).Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(rec.calls) != 1 || rec.calls[0].amount != 1250 {
		t.Errorf("calls = %+v, want a single 1250-byte correction", rec.calls)
	}
}

// An attachment-only mailbox has no message rows, so the attachment pass must be
// able to introduce a pubkey rather than only adding to existing ones.
func TestReconcileHandlesAttachmentOnlyMailbox(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	expectMeasurements(mock,
		messageRows(),
		messageRows().AddRow("bob", 700),
		recordedRows(),
	)

	rec := &fakeRecorder{}
	if _, err := New(db, rec, zap.NewNop()).Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(rec.calls) != 1 || rec.calls[0] != (recordedCall{"bob", 700}) {
		t.Errorf("calls = %+v, want {bob 700}", rec.calls)
	}
}

// One user's failure must not leave everyone else's quota stale.
func TestReconcileContinuesPastRecordFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	expectMeasurements(mock,
		messageRows().AddRow("alice", 1000).AddRow("bob", 2000),
		messageRows(),
		recordedRows(),
	)

	rec := &fakeRecorder{err: errors.New("quota table locked")}
	result, err := New(db, rec, zap.NewNop()).Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if result.Mailboxes != 2 {
		t.Errorf("Mailboxes = %d, want 2 — the pass should not abort", result.Mailboxes)
	}
	if result.Corrected != 0 {
		t.Errorf("Corrected = %d, want 0", result.Corrected)
	}
}

// Self-hosters have no platform quota tables; the pass must degrade quietly.
func TestReconcileToleratesMissingQuotaTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("FROM emails").WillReturnRows(messageRows().AddRow("alice", 100))
	mock.ExpectQuery("FROM attachments").WillReturnRows(messageRows())
	mock.ExpectQuery("FROM user_quota_usage").
		WillReturnError(errors.New(`relation "user_quota_usage" does not exist`))

	rec := &fakeRecorder{}
	result, err := New(db, rec, zap.NewNop()).Reconcile(t.Context())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Corrected != 1 || rec.calls[0].amount != 100 {
		t.Errorf("result = %+v calls = %+v, want a 100-byte correction", result, rec.calls)
	}
}

// A missing GRANT is an operator fix, not a code failure, and must be
// distinguishable so the log names the one-line remedy instead of burying it.
func TestReconcileReportsUnreadableUsageDistinctly(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("FROM emails").WillReturnRows(messageRows().AddRow("alice", 100))
	mock.ExpectQuery("FROM attachments").WillReturnRows(messageRows())
	mock.ExpectQuery("FROM user_quota_usage").
		WillReturnError(errors.New("pq: permission denied for table user_quota_usage (42501)"))

	rec := &fakeRecorder{}
	_, err = New(db, rec, zap.NewNop()).Reconcile(t.Context())
	if !errors.Is(err, ErrUsageUnreadable) {
		t.Fatalf("err = %v, want it to wrap ErrUsageUnreadable", err)
	}
	// Nothing must be reported off a measurement we could not compare against —
	// a blind absolute write would double-count every mailbox.
	if len(rec.calls) != 0 {
		t.Errorf("got %d RecordUsage calls, want 0", len(rec.calls))
	}
}
