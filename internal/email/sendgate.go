package email

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-common/platform"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/ratelimit"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
)

// Send-gate errors. These are CLIENT errors — the API layer maps them to 4xx,
// never 500.
var (
	// ErrSendNotPermitted: anonymous identities (extension-only or
	// auto-assigned-address-only) are receive-only. They can receive mail and
	// be zapped, but never send. This is the sybil control — infinite free keys
	// must not mean infinite free outbound.
	ErrSendNotPermitted = errors.New("sending requires a claimed address; anonymous identities are receive-only")

	// ErrSendSuspended: the suspend ladder has held or suspended this account.
	ErrSendSuspended = errors.New("sending is suspended for this account")
)

// RateLimitError reports which limit denied the send and when to retry.
type RateLimitError struct {
	Reason     ratelimit.Reason
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limit exceeded (%s); retry after %s", e.Reason, e.RetryAfter.Round(time.Second))
	}
	return fmt.Sprintf("rate limit exceeded (%s)", e.Reason)
}

// TierProvider resolves a pubkey's platform tier. Satisfied by *platform.Client;
// an interface so the gate is testable without the platform DB.
type TierProvider interface {
	GetTier(ctx context.Context, pubkey string) (platform.Tier, error)
}

// SendStateStore supplies the per-account state the gate needs.
type SendStateStore interface {
	GetMailboxSendState(ctx context.Context, pubkey string) (storage.SendState, error)
	GetAccountCreatedAt(ctx context.Context, pubkey string) (time.Time, bool, error)
}

// ThrottleLookup reports whether the abuse ladder has clamped this account.
// Satisfied by *abuse.RedisMarkStore; an interface so the gate does not depend
// on the ladder's storage.
type ThrottleLookup interface {
	Throttled(ctx context.Context, pubkey string) (bool, error)
}

// SendGate decides whether an outbound send is permitted, applying, in order:
// tier send-rights, the account's local send state, then rate limits.
//
// Order matters: a hard denial (anonymous / suspended) must not consume rate
// limit quota, so the cheap categorical checks run first.
type SendGate struct {
	tiers     TierProvider
	state     SendStateStore
	rlStore   ratelimit.Store
	limits    ratelimit.Limits
	throttles ThrottleLookup
	now       func() time.Time
}

// NewSendGate builds the gate over a shared rate-limit Store.
func NewSendGate(tiers TierProvider, state SendStateStore, rlStore ratelimit.Store, limits ratelimit.Limits) *SendGate {
	return &SendGate{tiers: tiers, state: state, rlStore: rlStore, limits: limits, now: time.Now}
}

// WithThrottles makes the gate honour the abuse ladder's throttle rung. Without
// it the ladder's hold and suspend rungs still bite (they flip send state in
// Postgres), but a throttle is inert.
func (g *SendGate) WithThrottles(t ThrottleLookup) *SendGate {
	g.throttles = t
	return g
}

// Check authorizes an outbound send of the given recipient count and size.
// Returns nil when the send may proceed (having consumed rate-limit quota).
func (g *SendGate) Check(ctx context.Context, pubkey string, recipients int, bytes int64) error {
	tier, err := g.tiers.GetTier(ctx, pubkey)
	if err != nil {
		return fmt.Errorf("tier lookup failed: %w", err)
	}
	if tier == platform.TierAnonymous {
		return ErrSendNotPermitted
	}

	state, err := g.state.GetMailboxSendState(ctx, pubkey)
	if err != nil {
		return fmt.Errorf("send state lookup failed: %w", err)
	}
	if !state.Enabled {
		return ErrSendSuspended
	}

	// The abuse ladder's throttle rung. A lookup failure is treated as
	// "not throttled": the mark store is a cache of a judgement, and a Redis
	// blip must not deny every send on the platform. The harder rungs live in
	// Postgres above, so nothing dangerous fails open here.
	throttled := false
	if g.throttles != nil {
		if t, terr := g.throttles.Throttled(ctx, pubkey); terr == nil {
			throttled = t
		}
	}

	// Paid tier or an operator/WoT elevation lifts the per-account windows;
	// domain-wide backstops and the size cap still apply. A throttle overrides
	// elevation — the ladder judged this specific account, and paying does not
	// buy out of that.
	elevated := (state.Elevated || tier == platform.TierPaid) && !throttled

	effective := g.limits
	switch {
	case throttled:
		effective = effective.Throttled()
	case elevated:
		effective = effective.Elevated()
	}

	newAccount := false
	if !elevated {
		created, ok, err := g.state.GetAccountCreatedAt(ctx, pubkey)
		if err != nil {
			return fmt.Errorf("account age lookup failed: %w", err)
		}
		if ok {
			newAccount = g.now().Sub(created) < time.Duration(g.limits.Domain.NewAccountDays)*24*time.Hour
		}
		if newAccount {
			// Named-but-new accounts get the tighter 20 recipients/day cap.
			effective.Account = ratelimit.NewAccountAccountLimits(effective.Account)
		}
	}

	decision, err := ratelimit.New(g.rlStore, effective).Allow(ctx, ratelimit.Send{
		Pubkey:     pubkey,
		Recipients: recipients,
		Bytes:      bytes,
		NewAccount: newAccount,
	})
	if err != nil {
		return fmt.Errorf("rate limit check failed: %w", err)
	}
	if !decision.Allowed {
		return &RateLimitError{Reason: decision.Reason, RetryAfter: decision.RetryAfter}
	}
	return nil
}
