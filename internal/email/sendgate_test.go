package email

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-common/platform"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/ratelimit"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
)

const gatePubkey = "ac16282f720514d926a57b5c13f02d1f4e32bd6fe3e00f713f50964571685f62"

type fakeTiers struct {
	tier platform.Tier
	err  error
}

func (f fakeTiers) GetTier(ctx context.Context, pubkey string) (platform.Tier, error) {
	return f.tier, f.err
}

type fakeState struct {
	state   storage.SendState
	created time.Time
	hasUser bool
	err     error
}

func (f fakeState) GetMailboxSendState(ctx context.Context, pubkey string) (storage.SendState, error) {
	return f.state, f.err
}
func (f fakeState) GetAccountCreatedAt(ctx context.Context, pubkey string) (time.Time, bool, error) {
	return f.created, f.hasUser, nil
}

func gate(tier platform.Tier, st fakeState) *SendGate {
	return NewSendGate(fakeTiers{tier: tier}, st, ratelimit.NewMemoryStore(), ratelimit.DefaultLimits())
}

func establishedNamed() fakeState {
	return fakeState{
		state:   storage.SendState{Enabled: true},
		created: time.Now().Add(-30 * 24 * time.Hour),
		hasUser: true,
	}
}

// The core sybil control: anonymous identities are receive-only.
func TestAnonymousIsReceiveOnly(t *testing.T) {
	err := gate(platform.TierAnonymous, establishedNamed()).Check(context.Background(), gatePubkey, 1, 100)
	if !errors.Is(err, ErrSendNotPermitted) {
		t.Fatalf("err = %v, want ErrSendNotPermitted", err)
	}
}

func TestNamedEstablishedMaySend(t *testing.T) {
	if err := gate(platform.TierNamed, establishedNamed()).Check(context.Background(), gatePubkey, 5, 100); err != nil {
		t.Fatalf("established named send denied: %v", err)
	}
}

// A suspended account is blocked regardless of tier.
func TestSuspendedAccountBlocked(t *testing.T) {
	st := establishedNamed()
	st.state.Enabled = false
	err := gate(platform.TierPaid, st).Check(context.Background(), gatePubkey, 1, 100)
	if !errors.Is(err, ErrSendSuspended) {
		t.Fatalf("err = %v, want ErrSendSuspended", err)
	}
}

// A hard denial must not burn rate-limit quota.
func TestHardDenialConsumesNoQuota(t *testing.T) {
	store := ratelimit.NewMemoryStore()
	g := NewSendGate(fakeTiers{tier: platform.TierAnonymous}, establishedNamed(), store, ratelimit.DefaultLimits())
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := g.Check(ctx, gatePubkey, 1, 100); !errors.Is(err, ErrSendNotPermitted) {
			t.Fatalf("expected ErrSendNotPermitted, got %v", err)
		}
	}
	// Same store, now a permitted sender: full budget must remain.
	g2 := NewSendGate(fakeTiers{tier: platform.TierNamed}, establishedNamed(), store, ratelimit.DefaultLimits())
	for i := 0; i < 5; i++ {
		if err := g2.Check(ctx, gatePubkey, 1, 100); err != nil {
			t.Fatalf("send %d denied (%v) — rejected sends leaked quota", i, err)
		}
	}
}

// Named-but-new (<7d) accounts are capped at 20 recipients/day.
func TestNewNamedAccountRecipientCap(t *testing.T) {
	st := establishedNamed()
	st.created = time.Now().Add(-24 * time.Hour) // 1 day old
	g := gate(platform.TierNamed, st)
	ctx := context.Background()

	if err := g.Check(ctx, gatePubkey, 20, 100); err != nil {
		t.Fatalf("20 recipients should be allowed for a new account: %v", err)
	}
	err := g.Check(ctx, gatePubkey, 1, 100)
	var rle *RateLimitError
	if !errors.As(err, &rle) || rle.Reason != ratelimit.ReasonAcctRecipDay {
		t.Fatalf("err = %v, want RateLimitError/%s", err, ratelimit.ReasonAcctRecipDay)
	}
}

// Paid tier is elevated: the per-account recipients/day cap does not apply.
func TestPaidTierIsElevated(t *testing.T) {
	st := establishedNamed()
	st.created = time.Now().Add(-24 * time.Hour) // new, but paid
	g := gate(platform.TierPaid, st)
	ctx := context.Background()

	// Well past the 20/day new-account cap and the 1000/day baseline.
	for i := 0; i < 30; i++ {
		if err := g.Check(ctx, gatePubkey, 50, 100); err != nil {
			t.Fatalf("paid send %d denied: %v", i, err)
		}
	}
}

// An operator-set elevation on the mailbox has the same effect as paid.
func TestOperatorElevationLiftsAccountLimits(t *testing.T) {
	st := establishedNamed()
	st.state.Elevated = true
	g := gate(platform.TierNamed, st)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		if err := g.Check(ctx, gatePubkey, 50, 100); err != nil {
			t.Fatalf("elevated send %d denied: %v", i, err)
		}
	}
}

// Rate-limit denials surface a typed error carrying the reason + retry hint,
// so the API can return 429 with Retry-After.
func TestRateLimitErrorCarriesRetryAfter(t *testing.T) {
	g := gate(platform.TierNamed, establishedNamed())
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := g.Check(ctx, gatePubkey, 1, 100); err != nil {
			t.Fatalf("send %d denied early: %v", i, err)
		}
	}
	err := g.Check(ctx, gatePubkey, 1, 100)
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if rle.Reason != ratelimit.ReasonAcctMsgMin {
		t.Errorf("reason = %s, want %s", rle.Reason, ratelimit.ReasonAcctMsgMin)
	}
	if rle.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want > 0", rle.RetryAfter)
	}
}

// An identity with no users row yet (pre-provisioning race) must not be treated
// as "new" and silently capped — absence of data is not evidence of newness.
func TestMissingUsersRowIsNotTreatedAsNew(t *testing.T) {
	st := establishedNamed()
	st.hasUser = false
	g := gate(platform.TierNamed, st)
	ctx := context.Background()
	// Baseline (1000/day) applies, not the 20/day new-account cap.
	if err := g.Check(ctx, gatePubkey, 50, 100); err != nil {
		t.Fatalf("denied: %v", err)
	}
	if err := g.Check(ctx, gatePubkey, 50, 100); err != nil {
		t.Fatalf("second send denied (%v) — treated as new account", err)
	}
}

// fakeThrottles stands in for the abuse ladder's mark store.
type fakeThrottles struct {
	throttled bool
	err       error
}

func (f fakeThrottles) Throttled(ctx context.Context, pubkey string) (bool, error) {
	return f.throttled, f.err
}

// A throttled account keeps sending — being wrongly flagged must not read as an
// outage — but at a rate that makes bulk spam pointless.
func TestThrottledAccountGetsClampedLimits(t *testing.T) {
	g := gate(platform.TierNamed, establishedNamed()).WithThrottles(fakeThrottles{throttled: true})
	ctx := context.Background()

	if err := g.Check(ctx, gatePubkey, 5, 100); err != nil {
		t.Fatalf("first throttled send denied: %v", err)
	}

	// Throttled limits allow 1 message/minute, so the second is refused.
	err := g.Check(ctx, gatePubkey, 1, 100)
	var rlErr *RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("err = %v, want a RateLimitError", err)
	}
	if rlErr.Reason != ratelimit.ReasonAcctMsgMin {
		t.Errorf("reason = %v, want %v", rlErr.Reason, ratelimit.ReasonAcctMsgMin)
	}
}

// Recipients-per-message drops to 5 under a throttle, which is what actually
// caps a spam blast.
func TestThrottledAccountRecipientCap(t *testing.T) {
	g := gate(platform.TierNamed, establishedNamed()).WithThrottles(fakeThrottles{throttled: true})

	err := g.Check(context.Background(), gatePubkey, 20, 100)
	var rlErr *RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("err = %v, want a RateLimitError", err)
	}
	if rlErr.Reason != ratelimit.ReasonRecipPerMsg {
		t.Errorf("reason = %v, want %v", rlErr.Reason, ratelimit.ReasonRecipPerMsg)
	}
}

// The ladder judged this specific account; paying does not buy out of it.
func TestThrottleOverridesElevation(t *testing.T) {
	st := establishedNamed()
	st.state.Elevated = true
	g := gate(platform.TierPaid, st).WithThrottles(fakeThrottles{throttled: true})
	ctx := context.Background()

	if err := g.Check(ctx, gatePubkey, 1, 100); err != nil {
		t.Fatalf("first throttled send denied: %v", err)
	}
	if err := g.Check(ctx, gatePubkey, 1, 100); err == nil {
		t.Error("elevated account escaped the throttle")
	}
}

// A mark-store outage must not deny every send on the platform: the hard rungs
// are enforced in Postgres, so nothing dangerous fails open here.
func TestThrottleLookupFailureFailsOpen(t *testing.T) {
	g := gate(platform.TierNamed, establishedNamed()).
		WithThrottles(fakeThrottles{err: errors.New("redis down")})

	if err := g.Check(context.Background(), gatePubkey, 5, 100); err != nil {
		t.Fatalf("send denied because the mark store was unavailable: %v", err)
	}
}

// Without a throttle mark the account keeps its normal limits.
func TestUnthrottledAccountUnaffected(t *testing.T) {
	g := gate(platform.TierNamed, establishedNamed()).WithThrottles(fakeThrottles{throttled: false})
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := g.Check(ctx, gatePubkey, 1, 100); err != nil {
			t.Fatalf("send %d denied under normal limits: %v", i, err)
		}
	}
}
