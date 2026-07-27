package abuse

import (
	"testing"
	"time"
)

func TestEvaluateRungs(t *testing.T) {
	th := DefaultThresholds()

	tests := []struct {
		name string
		sig  Signals
		want Rung
	}{
		{
			name: "clean sender",
			sig:  Signals{MessagesSent: 100, HardBounces: 1},
			want: RungNone,
		},
		{
			// Rates below the volume floor are noise: 2 of 5 is 40%, which would
			// otherwise trip the suspend threshold on a nearly idle account.
			name: "below volume floor is never judged",
			sig:  Signals{MessagesSent: 5, HardBounces: 2},
			want: RungNone,
		},
		{
			name: "warn threshold",
			sig:  Signals{MessagesSent: 100, HardBounces: 6},
			want: RungWarn,
		},
		{
			name: "throttle threshold",
			sig:  Signals{MessagesSent: 100, HardBounces: 20},
			want: RungThrottle,
		},
		{
			name: "hold threshold",
			sig:  Signals{MessagesSent: 100, HardBounces: 40},
			want: RungHold,
		},
		{
			name: "suspend threshold",
			sig:  Signals{MessagesSent: 100, HardBounces: 70},
			want: RungSuspend,
		},
		{
			// The compromised-account case: no bounces have come back yet, so
			// only velocity can catch it.
			name: "velocity holds even with no bounces",
			sig:  Signals{MessagesSent: 600, HardBounces: 0, RecipientsLastHour: 900},
			want: RungHold,
		},
		{
			// Velocity must fire below the volume floor too — a burst from a
			// fresh account is exactly what the tripwire is for.
			name: "velocity fires below the volume floor",
			sig:  Signals{MessagesSent: 3, RecipientsLastHour: 900},
			want: RungHold,
		},
		{
			name: "soft bounces alone are not actionable",
			sig:  Signals{MessagesSent: 100, SoftBounces: 90},
			want: RungNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := th.Evaluate(tt.sig)
			if got != tt.want {
				t.Errorf("Evaluate() = %v (%s), want %v", got, reason, tt.want)
			}
			if got != RungNone && reason == "" {
				t.Error("actionable rung returned without a reason")
			}
		})
	}
}

func TestHardBounceRateIgnoresIdleAccounts(t *testing.T) {
	s := Signals{MessagesSent: 0, HardBounces: 5}
	if got := s.HardBounceRate(); got != 0 {
		t.Errorf("HardBounceRate() = %v, want 0", got)
	}
}

func TestRungString(t *testing.T) {
	want := map[Rung]string{
		RungNone: "none", RungWarn: "warn", RungThrottle: "throttle",
		RungHold: "hold", RungSuspend: "suspend",
	}
	for rung, s := range want {
		if got := rung.String(); got != s {
			t.Errorf("Rung(%d).String() = %q, want %q", int(rung), got, s)
		}
	}
}

func TestMemoryMarkStoreExpiry(t *testing.T) {
	store := NewMemoryMarkStore()
	now := time.Now()
	store.now = func() time.Time { return now }

	ctx := t.Context()
	if err := store.Set(ctx, "pk", Mark{Rung: RungThrottle}, time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if _, ok, _ := store.Get(ctx, "pk"); !ok {
		t.Fatal("mark missing immediately after Set")
	}

	now = now.Add(2 * time.Hour)
	if _, ok, _ := store.Get(ctx, "pk"); ok {
		t.Error("mark survived past its TTL")
	}
}

// A zero TTL means the mark persists — correct for hold and suspend, which are
// backed by Postgres state that must not silently lapse.
func TestMemoryMarkStoreZeroTTLDoesNotExpire(t *testing.T) {
	store := NewMemoryMarkStore()
	now := time.Now()
	store.now = func() time.Time { return now }

	ctx := t.Context()
	if err := store.Set(ctx, "pk", Mark{Rung: RungHold}, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	now = now.Add(30 * 24 * time.Hour)
	if _, ok, _ := store.Get(ctx, "pk"); !ok {
		t.Error("hold mark expired despite a zero TTL")
	}
}

func TestMemoryMarkStoreThrottled(t *testing.T) {
	store := NewMemoryMarkStore()
	ctx := t.Context()

	for _, tc := range []struct {
		rung Rung
		want bool
	}{
		{RungWarn, false},
		{RungThrottle, true},
		{RungHold, true},
		{RungSuspend, true},
	} {
		if err := store.Set(ctx, "pk", Mark{Rung: tc.rung}, time.Hour); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, err := store.Throttled(ctx, "pk")
		if err != nil {
			t.Fatalf("Throttled: %v", err)
		}
		if got != tc.want {
			t.Errorf("Throttled() at %v = %v, want %v", tc.rung, got, tc.want)
		}
	}
}

func TestMemoryMarkStoreThrottledWhenUnmarked(t *testing.T) {
	got, err := NewMemoryMarkStore().Throttled(t.Context(), "pk")
	if err != nil {
		t.Fatalf("Throttled: %v", err)
	}
	if got {
		t.Error("unmarked account reported as throttled")
	}
}

func TestEvaluateComplaintRate(t *testing.T) {
	th := DefaultThresholds()

	tests := []struct {
		name string
		sig  Signals
		want Rung
	}{
		{
			// 0.05%: below every threshold. Bulk senders live here.
			name: "clean complaint rate",
			sig:  Signals{MessagesSent: 100, RecipientsSent: 2000, Complaints: 1},
			want: RungNone,
		},
		{
			name: "below recipient floor is never judged",
			sig:  Signals{MessagesSent: 100, RecipientsSent: 100, Complaints: 5},
			want: RungNone,
		},
		{
			// 0.1%
			name: "warn complaint rate",
			sig:  Signals{MessagesSent: 100, RecipientsSent: 2000, Complaints: 2},
			want: RungWarn,
		},
		{
			// 0.3%
			name: "throttle complaint rate",
			sig:  Signals{MessagesSent: 100, RecipientsSent: 2000, Complaints: 6},
			want: RungThrottle,
		},
		{
			// 0.5%
			name: "hold complaint rate",
			sig:  Signals{MessagesSent: 100, RecipientsSent: 2000, Complaints: 10},
			want: RungHold,
		},
		{
			// Complaint rate is indirect evidence of unwanted mail, not fraud;
			// it must never reach the platform-wide hammer on its own.
			name: "complaints never warrant suspend",
			sig:  Signals{MessagesSent: 100, RecipientsSent: 2000, Complaints: 500},
			want: RungHold,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := th.Evaluate(tt.sig)
			if got != tt.want {
				t.Errorf("Evaluate() = %v (%s), want %v", got, reason, tt.want)
			}
		})
	}
}

// A clean bounce rate says nothing about whether recipients want the mail, so
// the signals must not cancel each other out.
func TestEvaluateTakesTheHigherOfBounceAndComplaintRungs(t *testing.T) {
	th := DefaultThresholds()

	// Bounce rate 1% (clean), complaint rate 0.5% (hold).
	got, reason := th.Evaluate(Signals{
		MessagesSent: 100, HardBounces: 1, RecipientsSent: 2000, Complaints: 10,
	})
	if got != RungHold {
		t.Errorf("Evaluate() = %v (%s), want %v", got, reason, RungHold)
	}

	// And the reverse: bounce rate 40% (hold), complaint rate 0% (clean).
	got, reason = th.Evaluate(Signals{
		MessagesSent: 100, HardBounces: 40, RecipientsSent: 2000,
	})
	if got != RungHold {
		t.Errorf("Evaluate() = %v (%s), want %v", got, reason, RungHold)
	}
}

// The denominator is recipients, not messages: one message to 500 people
// drawing 5 complaints is 1%, not 500%.
func TestComplaintRateIsPerRecipient(t *testing.T) {
	s := Signals{MessagesSent: 1, RecipientsSent: 500, Complaints: 5}
	if got := s.ComplaintRate(); got != 0.01 {
		t.Errorf("ComplaintRate() = %v, want 0.01", got)
	}
}

func TestComplaintRateIgnoresIdleAccounts(t *testing.T) {
	s := Signals{RecipientsSent: 0, Complaints: 5}
	if got := s.ComplaintRate(); got != 0 {
		t.Errorf("ComplaintRate() = %v, want 0", got)
	}
}
