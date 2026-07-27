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
