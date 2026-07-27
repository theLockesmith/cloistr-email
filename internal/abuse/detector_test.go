package abuse

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"go.uber.org/zap"
)

type stubSignals struct {
	signals []Signals
	err     error
}

func (s stubSignals) Collect(context.Context, time.Duration) ([]Signals, error) {
	return s.signals, s.err
}

func TestScanAppliesRungsPerAccount(t *testing.T) {
	cfg := DefaultConfig()
	ladder, _, state, _ := newTestLadder(cfg)

	src := stubSignals{signals: []Signals{
		{Pubkey: "clean", MessagesSent: 100, HardBounces: 1},
		{Pubkey: "warned", MessagesSent: 100, HardBounces: 8},
		{Pubkey: "throttled", MessagesSent: 100, HardBounces: 20},
		{Pubkey: "held", MessagesSent: 100, HardBounces: 40},
	}}

	result, err := NewDetector(src, ladder, cfg, zap.NewNop()).Scan(t.Context())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if result.Evaluated != 4 {
		t.Errorf("Evaluated = %d, want 4", result.Evaluated)
	}
	if result.Actioned != 3 {
		t.Errorf("Actioned = %d, want 3", result.Actioned)
	}
	if result.ByRung[RungWarn] != 1 || result.ByRung[RungThrottle] != 1 || result.ByRung[RungHold] != 1 {
		t.Errorf("ByRung = %v, want one each of warn/throttle/hold", result.ByRung)
	}

	if state.enabled["held"] {
		t.Error("held account's send gate was not closed")
	}
	if _, touched := state.enabled["throttled"]; touched {
		t.Error("throttled account's send gate was touched")
	}
}

// One account failing to enforce must not stop the rest of the pass, or a single
// bad row leaves every other abuser unenforced.
func TestScanContinuesPastPerAccountFailure(t *testing.T) {
	cfg := DefaultConfig()
	marks := NewMemoryMarkStore()
	state := newFakeState()
	state.err = errors.New("db down")
	ladder := NewLadder(cfg, marks, state, &fakeQueue{}, zap.NewNop())

	src := stubSignals{signals: []Signals{
		{Pubkey: "held-a", MessagesSent: 100, HardBounces: 40},
		{Pubkey: "held-b", MessagesSent: 100, HardBounces: 40},
	}}

	result, err := NewDetector(src, ladder, cfg, zap.NewNop()).Scan(t.Context())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.Evaluated != 2 {
		t.Errorf("Evaluated = %d, want 2 — the pass should not abort", result.Evaluated)
	}
	if result.Actioned != 0 {
		t.Errorf("Actioned = %d, want 0 — enforcement failed for both", result.Actioned)
	}
}

func TestScanSkipsEmptyPubkeys(t *testing.T) {
	cfg := DefaultConfig()
	ladder, _, _, _ := newTestLadder(cfg)

	src := stubSignals{signals: []Signals{{Pubkey: "", MessagesSent: 100, HardBounces: 90}}}

	result, err := NewDetector(src, ladder, cfg, zap.NewNop()).Scan(t.Context())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.Evaluated != 0 {
		t.Errorf("Evaluated = %d, want 0", result.Evaluated)
	}
}

func TestScanPropagatesCollectFailure(t *testing.T) {
	cfg := DefaultConfig()
	ladder, _, _, _ := newTestLadder(cfg)

	src := stubSignals{err: errors.New("query failed")}

	if _, err := NewDetector(src, ladder, cfg, zap.NewNop()).Scan(t.Context()); err == nil {
		t.Error("Scan swallowed a signal-collection failure")
	}
}

func TestPostgresSignalsJoinsSendsAndBounces(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("FROM outbound_queue").
		WillReturnRows(sqlmock.NewRows([]string{"pubkey", "messages", "recipients", "recipients_last_hour"}).
			AddRow("alice", 120, 300, 40).
			AddRow("bob", 10, 12, 2))

	mock.ExpectQuery("FROM email_bounces").
		WillReturnRows(sqlmock.NewRows([]string{"sender_pubkey", "hard", "soft"}).
			AddRow("alice", 45, 3).
			// A bounce from an account with no sends in the window has no
			// denominator, so it must be dropped rather than invented.
			AddRow("carol", 99, 0))

	mock.ExpectQuery("FROM email_complaints").
		WillReturnRows(sqlmock.NewRows([]string{"sender_pubkey", "complaints"}).
			AddRow("alice", 4))

	got, err := NewPostgresSignals(db).Collect(t.Context(), 24*time.Hour)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d signal sets, want 2", len(got))
	}

	byPubkey := map[string]Signals{}
	for _, s := range got {
		byPubkey[s.Pubkey] = s
	}

	alice := byPubkey["alice"]
	if alice.MessagesSent != 120 || alice.HardBounces != 45 || alice.RecipientsLastHour != 40 {
		t.Errorf("alice = %+v, want messages=120 hard=45 lastHour=40", alice)
	}
	if alice.Complaints != 4 {
		t.Errorf("alice.Complaints = %d, want 4", alice.Complaints)
	}
	if _, ok := byPubkey["carol"]; ok {
		t.Error("carol has bounces but no sends and should not be judged")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// A self-hoster who has not run migration 009 must get a quiet no-op, not a
// crash loop.
func TestPostgresSignalsToleratesMissingTables(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta("FROM outbound_queue")).
		WillReturnError(errors.New(`relation "outbound_queue" does not exist`))

	got, err := NewPostgresSignals(db).Collect(t.Context(), 24*time.Hour)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d signal sets, want 0", len(got))
	}
}

// email_complaints only exists once migration 010 is applied, so on any
// deployment running ahead of that migration the first account with sends will
// drive this query against a table that is not there. Missing-relation
// tolerance for outbound_queue is not enough: Collect early-returns at zero
// senders, so the complaint path is only reached once real traffic exists.
func TestPostgresSignalsToleratesMissingComplaintsTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta("FROM outbound_queue")).
		WillReturnRows(sqlmock.NewRows([]string{"pubkey", "messages", "recipients", "recipients_last_hour"}).
			AddRow("alice", 50, 80, 5))
	mock.ExpectQuery(regexp.QuoteMeta("FROM email_bounces")).
		WillReturnRows(sqlmock.NewRows([]string{"sender_pubkey", "hard", "soft"}).AddRow("alice", 2, 0))
	mock.ExpectQuery(regexp.QuoteMeta("FROM email_complaints")).
		WillReturnError(errors.New(`relation "email_complaints" does not exist`))

	got, err := NewPostgresSignals(db).Collect(t.Context(), 24*time.Hour)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d signal sets, want 1", len(got))
	}
	// The bounce signal must survive: an unapplied complaints migration cannot
	// be allowed to disable bounce-rate enforcement.
	if got[0].HardBounces != 2 {
		t.Errorf("HardBounces = %d, want 2", got[0].HardBounces)
	}
	if got[0].Complaints != 0 {
		t.Errorf("Complaints = %d, want 0", got[0].Complaints)
	}
}

// Likewise for a missing email_bounces: the pass must keep going rather than
// abort and leave velocity enforcement dead too.
func TestPostgresSignalsToleratesMissingBouncesTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta("FROM outbound_queue")).
		WillReturnRows(sqlmock.NewRows([]string{"pubkey", "messages", "recipients", "recipients_last_hour"}).
			AddRow("alice", 50, 900, 900))
	mock.ExpectQuery(regexp.QuoteMeta("FROM email_bounces")).
		WillReturnError(errors.New(`relation "email_bounces" does not exist`))
	mock.ExpectQuery(regexp.QuoteMeta("FROM email_complaints")).
		WillReturnRows(sqlmock.NewRows([]string{"sender_pubkey", "complaints"}))

	got, err := NewPostgresSignals(db).Collect(t.Context(), 24*time.Hour)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 || got[0].RecipientsLastHour != 900 {
		t.Fatalf("got %+v, want the velocity signal to survive", got)
	}
}
