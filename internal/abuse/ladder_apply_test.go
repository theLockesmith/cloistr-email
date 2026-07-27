package abuse

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

// fakeState records the send-state flips the ladder performs.
type fakeState struct {
	enabled     map[string]bool
	suspendedAt map[string]*time.Time
	err         error
}

func newFakeState() *fakeState {
	return &fakeState{enabled: map[string]bool{}, suspendedAt: map[string]*time.Time{}}
}

func (f *fakeState) SetMailboxSendEnabled(_ context.Context, pubkey string, enabled bool, at *time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.enabled[pubkey] = enabled
	f.suspendedAt[pubkey] = at
	return nil
}

// fakeQueue records hold/release calls.
type fakeQueue struct {
	held     int
	released int
	holdErr  error
}

func (f *fakeQueue) HoldForSender(context.Context, string) (int64, error) {
	if f.holdErr != nil {
		return 0, f.holdErr
	}
	f.held++
	return 3, nil
}

func (f *fakeQueue) ReleaseForSender(context.Context, string) (int64, error) {
	f.released++
	return 3, nil
}

func newTestLadder(cfg Config) (*Ladder, *MemoryMarkStore, *fakeState, *fakeQueue) {
	marks := NewMemoryMarkStore()
	state := newFakeState()
	queue := &fakeQueue{}
	return NewLadder(cfg, marks, state, queue, zap.NewNop()), marks, state, queue
}

func TestApplyHoldClosesGateAndParksQueue(t *testing.T) {
	ladder, marks, state, queue := newTestLadder(DefaultConfig())
	ctx := t.Context()

	applied, err := ladder.Apply(ctx, "pk", RungHold, "bounce rate")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied != RungHold {
		t.Errorf("applied = %v, want %v", applied, RungHold)
	}
	if state.enabled["pk"] {
		t.Error("send state still enabled after a hold")
	}
	if state.suspendedAt["pk"] == nil {
		t.Error("suspended_at not stamped on hold")
	}
	if queue.held != 1 {
		t.Errorf("queue holds = %d, want 1", queue.held)
	}

	m, ok, _ := marks.Get(ctx, "pk")
	if !ok || m.Rung != RungHold {
		t.Errorf("mark = %+v (present=%v), want hold", m, ok)
	}
}

// The default policy must not let the ladder revoke platform-wide access on its
// own — it tops out at hold and leaves the decision to a human.
func TestApplySuspendClampsToHoldByDefault(t *testing.T) {
	ladder, marks, state, _ := newTestLadder(DefaultConfig())
	ctx := t.Context()

	applied, err := ladder.Apply(ctx, "pk", RungSuspend, "very high bounce rate")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied != RungHold {
		t.Errorf("applied = %v, want %v (auto-suspend is off by default)", applied, RungHold)
	}
	if state.enabled["pk"] {
		t.Error("send state still enabled after a clamped suspend")
	}

	m, _, _ := marks.Get(ctx, "pk")
	if m.Rung != RungHold {
		t.Errorf("mark rung = %v, want %v", m.Rung, RungHold)
	}
}

func TestApplySuspendWhenOperatorOptsIn(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Thresholds.AllowAutoSuspend = true

	ladder, _, state, _ := newTestLadder(cfg)

	applied, err := ladder.Apply(t.Context(), "pk", RungSuspend, "very high bounce rate")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied != RungSuspend {
		t.Errorf("applied = %v, want %v", applied, RungSuspend)
	}
	if state.enabled["pk"] {
		t.Error("send state still enabled after a suspend")
	}
}

func TestApplyThrottleDoesNotTouchSendState(t *testing.T) {
	ladder, marks, state, queue := newTestLadder(DefaultConfig())
	ctx := t.Context()

	if _, err := ladder.Apply(ctx, "pk", RungThrottle, "bounce rate"); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if _, touched := state.enabled["pk"]; touched {
		t.Error("throttle flipped the send state; it should only clamp limits")
	}
	if queue.held != 0 {
		t.Error("throttle parked queued mail; only hold should do that")
	}

	throttled, err := marks.Throttled(ctx, "pk")
	if err != nil {
		t.Fatalf("Throttled: %v", err)
	}
	if !throttled {
		t.Error("throttle mark not visible to the send gate")
	}
}

func TestApplyClearsMarkWhenSignalsGoClean(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinHoldDuration = 0

	ladder, marks, state, queue := newTestLadder(cfg)
	ctx := t.Context()

	if _, err := ladder.Apply(ctx, "pk", RungHold, "bounce rate"); err != nil {
		t.Fatalf("Apply hold: %v", err)
	}

	applied, err := ladder.Apply(ctx, "pk", RungNone, "")
	if err != nil {
		t.Fatalf("Apply none: %v", err)
	}
	if applied != RungNone {
		t.Errorf("applied = %v, want %v", applied, RungNone)
	}
	if !state.enabled["pk"] {
		t.Error("send state not re-enabled on release")
	}
	if state.suspendedAt["pk"] != nil {
		t.Error("suspended_at not cleared on release")
	}
	if queue.released != 1 {
		t.Errorf("queue releases = %d, want 1", queue.released)
	}
	if _, ok, _ := marks.Get(ctx, "pk"); ok {
		t.Error("mark survived a clean evaluation")
	}
}

// A hold must not lift the moment the burst that caused it stops, or an abuser
// can oscillate in and out of enforcement between scans.
func TestApplyHonoursMinHoldDuration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinHoldDuration = time.Hour

	ladder, _, state, queue := newTestLadder(cfg)
	ctx := t.Context()

	if _, err := ladder.Apply(ctx, "pk", RungHold, "velocity"); err != nil {
		t.Fatalf("Apply hold: %v", err)
	}

	applied, err := ladder.Apply(ctx, "pk", RungNone, "")
	if err != nil {
		t.Fatalf("Apply none: %v", err)
	}
	if applied != RungHold {
		t.Errorf("applied = %v, want the hold to persist", applied)
	}
	if state.enabled["pk"] {
		t.Error("hold released before MinHoldDuration elapsed")
	}
	if queue.released != 0 {
		t.Error("queued mail unparked before MinHoldDuration elapsed")
	}
}

func TestApplyRespectsAutoReleaseHoldDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoReleaseHold = false
	cfg.MinHoldDuration = 0

	ladder, _, state, _ := newTestLadder(cfg)
	ctx := t.Context()

	if _, err := ladder.Apply(ctx, "pk", RungHold, "bounce rate"); err != nil {
		t.Fatalf("Apply hold: %v", err)
	}
	applied, err := ladder.Apply(ctx, "pk", RungNone, "")
	if err != nil {
		t.Fatalf("Apply none: %v", err)
	}

	if applied != RungHold {
		t.Errorf("applied = %v, want the hold to persist", applied)
	}
	if state.enabled["pk"] {
		t.Error("hold auto-released despite AutoReleaseHold=false")
	}
}

// Reaching a suspend took a deliberate decision; undoing it must too.
func TestApplyNeverAutoReleasesSuspend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Thresholds.AllowAutoSuspend = true
	cfg.MinHoldDuration = 0

	ladder, _, state, _ := newTestLadder(cfg)
	ctx := t.Context()

	if _, err := ladder.Apply(ctx, "pk", RungSuspend, "bounce rate"); err != nil {
		t.Fatalf("Apply suspend: %v", err)
	}
	applied, err := ladder.Apply(ctx, "pk", RungNone, "")
	if err != nil {
		t.Fatalf("Apply none: %v", err)
	}

	if applied != RungSuspend {
		t.Errorf("applied = %v, want the suspend to persist", applied)
	}
	if state.enabled["pk"] {
		t.Error("suspend auto-released")
	}
}

func TestApplyDeescalatesToLowerRung(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinHoldDuration = 0

	ladder, marks, state, _ := newTestLadder(cfg)
	ctx := t.Context()

	if _, err := ladder.Apply(ctx, "pk", RungHold, "bounce rate"); err != nil {
		t.Fatalf("Apply hold: %v", err)
	}

	applied, err := ladder.Apply(ctx, "pk", RungWarn, "improving")
	if err != nil {
		t.Fatalf("Apply warn: %v", err)
	}
	if applied != RungWarn {
		t.Errorf("applied = %v, want %v", applied, RungWarn)
	}
	if !state.enabled["pk"] {
		t.Error("send state not re-enabled when de-escalating below hold")
	}

	m, ok, _ := marks.Get(ctx, "pk")
	if !ok || m.Rung != RungWarn {
		t.Errorf("mark = %+v (present=%v), want warn", m, ok)
	}
}

// The send gate is already closed by the time the queue is parked, so a queue
// failure must not abandon the hold.
func TestApplyHoldSurvivesQueueFailure(t *testing.T) {
	marks := NewMemoryMarkStore()
	state := newFakeState()
	queue := &fakeQueue{holdErr: errors.New("queue down")}
	ladder := NewLadder(DefaultConfig(), marks, state, queue, zap.NewNop())

	applied, err := ladder.Apply(t.Context(), "pk", RungHold, "bounce rate")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied != RungHold {
		t.Errorf("applied = %v, want %v", applied, RungHold)
	}
	if state.enabled["pk"] {
		t.Error("hold abandoned because the queue was unavailable")
	}
}

func TestApplyFailsWhenSendStateUnwritable(t *testing.T) {
	marks := NewMemoryMarkStore()
	state := newFakeState()
	state.err = errors.New("db down")
	ladder := NewLadder(DefaultConfig(), marks, state, &fakeQueue{}, zap.NewNop())

	if _, err := ladder.Apply(t.Context(), "pk", RungHold, "bounce rate"); err == nil {
		t.Error("Apply reported success despite being unable to close the gate")
	}
	if _, ok, _ := marks.Get(t.Context(), "pk"); ok {
		t.Error("mark recorded even though enforcement failed")
	}
}

func TestApplyIsIdempotentAtTheSameRung(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinHoldDuration = time.Hour

	ladder, _, _, queue := newTestLadder(cfg)
	ctx := t.Context()

	for i := 0; i < 3; i++ {
		if _, err := ladder.Apply(ctx, "pk", RungHold, "bounce rate"); err != nil {
			t.Fatalf("Apply %d: %v", i, err)
		}
	}
	if queue.held != 1 {
		t.Errorf("queue holds = %d, want 1 — re-applying a hold should not re-park", queue.held)
	}
}

// A hold that is still warranted must be renewed, never released and re-taken.
// With MinHoldDuration elapsed, a naive "applied <= current" de-escalation check
// would re-open the gate on the very scan that reconfirmed the abuse.
func TestApplyRenewsRatherThanReleasingStillWarrantedHold(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinHoldDuration = 0

	ladder, marks, state, queue := newTestLadder(cfg)
	ctx := t.Context()

	if _, err := ladder.Apply(ctx, "pk", RungHold, "bounce rate"); err != nil {
		t.Fatalf("Apply hold: %v", err)
	}

	applied, err := ladder.Apply(ctx, "pk", RungHold, "bounce rate still high")
	if err != nil {
		t.Fatalf("Apply hold again: %v", err)
	}
	if applied != RungHold {
		t.Errorf("applied = %v, want %v", applied, RungHold)
	}
	if state.enabled["pk"] {
		t.Error("re-confirming a hold re-opened the send gate")
	}
	if queue.released != 0 {
		t.Errorf("queue releases = %d, want 0 — a renewed hold must not unpark mail", queue.released)
	}

	m, ok, _ := marks.Get(ctx, "pk")
	if !ok || m.Rung != RungHold {
		t.Errorf("mark = %+v (present=%v), want hold", m, ok)
	}
	if m.Reason != "bounce rate still high" {
		t.Errorf("mark reason = %q, want the refreshed reason", m.Reason)
	}
}

// The renewal must not stamp At forward, or a hold under continuous evaluation
// could never become eligible for auto-release.
func TestRenewalPreservesHoldStartTime(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinHoldDuration = time.Hour

	ladder, marks, state, _ := newTestLadder(cfg)
	ctx := t.Context()

	start := time.Now()
	ladder.now = func() time.Time { return start }
	if _, err := ladder.Apply(ctx, "pk", RungHold, "velocity"); err != nil {
		t.Fatalf("Apply hold: %v", err)
	}

	// Half an hour later the abuse is still visible: renew.
	ladder.now = func() time.Time { return start.Add(30 * time.Minute) }
	if _, err := ladder.Apply(ctx, "pk", RungHold, "velocity"); err != nil {
		t.Fatalf("Apply hold again: %v", err)
	}

	m, _, _ := marks.Get(ctx, "pk")
	if !m.At.Equal(start) {
		t.Errorf("mark At = %v, want the original hold time %v", m.At, start)
	}

	// Ninety minutes after the ORIGINAL hold, clean signals must release it.
	ladder.now = func() time.Time { return start.Add(90 * time.Minute) }
	applied, err := ladder.Apply(ctx, "pk", RungNone, "")
	if err != nil {
		t.Fatalf("Apply none: %v", err)
	}
	if applied != RungNone {
		t.Errorf("applied = %v, want %v", applied, RungNone)
	}
	if !state.enabled["pk"] {
		t.Error("hold not released despite MinHoldDuration having elapsed since the original hold")
	}
}

func TestReleaseManualLiftsSuspend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Thresholds.AllowAutoSuspend = true

	ladder, marks, state, queue := newTestLadder(cfg)
	ctx := t.Context()

	if _, err := ladder.Apply(ctx, "pk", RungSuspend, "bounce rate"); err != nil {
		t.Fatalf("Apply suspend: %v", err)
	}
	if err := ladder.ReleaseManual(ctx, "pk"); err != nil {
		t.Fatalf("ReleaseManual: %v", err)
	}

	if !state.enabled["pk"] {
		t.Error("send state not re-enabled by a manual release")
	}
	if queue.released != 1 {
		t.Errorf("queue releases = %d, want 1", queue.released)
	}
	if _, ok, _ := marks.Get(ctx, "pk"); ok {
		t.Error("mark survived a manual release")
	}
}
