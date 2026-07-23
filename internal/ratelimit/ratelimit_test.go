package ratelimit

import (
	"context"
	"testing"
	"time"
)

const testPubkey = "ac16282f720514d926a57b5c13f02d1f4e32bd6fe3e00f713f50964571685f62"

func newTestLimiter(t *testing.T, limits Limits) (*Limiter, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)}
	store := NewMemoryStore()
	store.now = clk.Now
	l := New(store, limits)
	l.now = clk.Now
	return l, clk
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time      { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func send(recipients int) Send {
	return Send{Pubkey: testPubkey, Recipients: recipients, Bytes: 1024}
}

// A rejected send must not consume quota — otherwise a client retrying a
// too-large message would burn its whole daily budget.
func TestDenialDoesNotConsumeQuota(t *testing.T) {
	l, _ := newTestLimiter(t, DefaultLimits())
	ctx := context.Background()

	// Exceeds recipients-per-message (50) — denied before any counter moves.
	d, err := l.Allow(ctx, send(51))
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if d.Allowed || d.Reason != ReasonRecipPerMsg {
		t.Fatalf("got %+v, want denied/%s", d, ReasonRecipPerMsg)
	}

	// The per-minute budget must be untouched: 5 sends should still succeed.
	for i := 0; i < 5; i++ {
		d, err := l.Allow(ctx, send(1))
		if err != nil {
			t.Fatalf("Allow %d: %v", i, err)
		}
		if !d.Allowed {
			t.Fatalf("send %d denied (%s) — denial leaked quota", i, d.Reason)
		}
	}
}

func TestMessagesPerMinute(t *testing.T) {
	l, clk := newTestLimiter(t, DefaultLimits())
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if d, _ := l.Allow(ctx, send(1)); !d.Allowed {
			t.Fatalf("send %d should be allowed, got %s", i, d.Reason)
		}
	}
	d, _ := l.Allow(ctx, send(1))
	if d.Allowed || d.Reason != ReasonAcctMsgMin {
		t.Fatalf("6th send: got %+v, want denied/%s", d, ReasonAcctMsgMin)
	}
	if d.RetryAfter <= 0 || d.RetryAfter > time.Minute {
		t.Errorf("RetryAfter = %v, want (0, 1m]", d.RetryAfter)
	}

	// Next minute window: allowed again.
	clk.Advance(time.Minute)
	if d, _ := l.Allow(ctx, send(1)); !d.Allowed {
		t.Fatalf("after window roll: denied (%s)", d.Reason)
	}
}

// Recipients, not messages, are the real spam metric — a few messages with many
// recipients each must hit the daily recipient cap.
func TestRecipientsPerDayCap(t *testing.T) {
	limits := DefaultLimits()
	limits.Account.MsgPerMin = Window{Max: 1000, Period: time.Minute}
	limits.Account.MsgPerHour = Window{Max: 1000, Period: time.Hour}
	l, _ := newTestLimiter(t, limits)
	ctx := context.Background()

	// 20 messages x 50 recipients = 1000, exactly at the cap.
	for i := 0; i < 20; i++ {
		if d, _ := l.Allow(ctx, send(50)); !d.Allowed {
			t.Fatalf("msg %d denied (%s)", i, d.Reason)
		}
	}
	d, _ := l.Allow(ctx, send(1))
	if d.Allowed || d.Reason != ReasonAcctRecipDay {
		t.Fatalf("got %+v, want denied/%s", d, ReasonAcctRecipDay)
	}
}

func TestMessageTooLarge(t *testing.T) {
	l, _ := newTestLimiter(t, DefaultLimits())
	d, _ := l.Allow(context.Background(), Send{Pubkey: testPubkey, Recipients: 1, Bytes: 26 * 1024 * 1024})
	if d.Allowed || d.Reason != ReasonMessageTooLarge {
		t.Fatalf("got %+v, want denied/%s", d, ReasonMessageTooLarge)
	}
}

// Elevated (paid / WoT-vouched) lifts per-account windows but must NOT lift the
// domain-wide backstop or the size cap.
func TestElevatedLiftsAccountButNotDomain(t *testing.T) {
	limits := DefaultLimits().Elevated()
	limits.Domain.MsgPerMin = Window{Max: 3, Period: time.Minute}
	l, _ := newTestLimiter(t, limits)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if d, _ := l.Allow(ctx, send(500)); !d.Allowed {
			t.Fatalf("elevated send %d denied (%s)", i, d.Reason)
		}
	}
	if d, _ := l.Allow(ctx, send(1)); d.Allowed || d.Reason != ReasonDomainMsgMin {
		t.Fatalf("got %+v, want denied/%s", d, ReasonDomainMsgMin)
	}

	// Size cap still applies to elevated accounts.
	if d, _ := l.Allow(ctx, Send{Pubkey: testPubkey, Recipients: 1, Bytes: 26 * 1024 * 1024}); d.Allowed {
		t.Error("elevated account bypassed the message size cap")
	}
}

// New (<7d) accounts get a 20 recipients/day cap per the send-rights table.
func TestNewAccountRecipientCap(t *testing.T) {
	limits := DefaultLimits()
	limits.Account = NewAccountAccountLimits(limits.Account)
	l, _ := newTestLimiter(t, limits)
	ctx := context.Background()

	if d, _ := l.Allow(ctx, Send{Pubkey: testPubkey, Recipients: 20, Bytes: 10, NewAccount: true}); !d.Allowed {
		t.Fatalf("20 recipients should be allowed for a new account: %s", d.Reason)
	}
	d, _ := l.Allow(ctx, Send{Pubkey: testPubkey, Recipients: 1, Bytes: 10, NewAccount: true})
	if d.Allowed || d.Reason != ReasonAcctRecipDay {
		t.Fatalf("got %+v, want denied/%s", d, ReasonAcctRecipDay)
	}
}

// Limits are per-pubkey: one account exhausting its budget must not affect another.
func TestLimitsArePerPubkey(t *testing.T) {
	l, _ := newTestLimiter(t, DefaultLimits())
	ctx := context.Background()
	other := "bb16282f720514d926a57b5c13f02d1f4e32bd6fe3e00f713f50964571685f62"

	for i := 0; i < 5; i++ {
		l.Allow(ctx, send(1))
	}
	if d, _ := l.Allow(ctx, send(1)); d.Allowed {
		t.Fatal("first account should be exhausted")
	}
	if d, _ := l.Allow(ctx, Send{Pubkey: other, Recipients: 1, Bytes: 10}); !d.Allowed {
		t.Fatalf("second account denied (%s) — limits leaked across pubkeys", d.Reason)
	}
}

// A window with Max <= 0 is disabled (self-hoster "unlimited").
func TestDisabledWindowsAreUnlimited(t *testing.T) {
	limits := Limits{} // everything zero => all windows disabled
	l, _ := newTestLimiter(t, limits)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		if d, _ := l.Allow(ctx, send(10_000)); !d.Allowed {
			t.Fatalf("send %d denied (%s) with all limits disabled", i, d.Reason)
		}
	}
}
