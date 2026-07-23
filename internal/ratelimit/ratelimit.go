package ratelimit

import (
	"context"
	"fmt"
	"time"
)

// Store is the counter backend. IncrBy atomically adds n to the counter at key,
// setting ttl on first write, and returns the new total. Implementations must be
// safe across replicas (the Redis one uses a single atomic script).
type Store interface {
	IncrBy(ctx context.Context, key string, n int64, ttl time.Duration) (int64, error)
	// PeekAll returns current values for keys (missing = 0). Used to evaluate
	// limits without consuming quota.
	PeekAll(ctx context.Context, keys []string) (map[string]int64, error)
}

// Reason identifies which limit denied a send.
type Reason string

const (
	ReasonNone            Reason = ""
	ReasonAcctMsgMin      Reason = "account_messages_per_minute"
	ReasonAcctMsgHour     Reason = "account_messages_per_hour"
	ReasonAcctMsgDay      Reason = "account_messages_per_day"
	ReasonRecipPerMsg     Reason = "recipients_per_message"
	ReasonAcctRecipDay    Reason = "account_recipients_per_day"
	ReasonMessageTooLarge Reason = "message_too_large"
	ReasonDomainMsgMin    Reason = "domain_messages_per_minute"
	ReasonDomainRecipHour Reason = "domain_recipients_per_hour"
	ReasonNewAcctRecipDay Reason = "new_account_recipients_per_day"
)

// Decision is the outcome of a limit check.
type Decision struct {
	Allowed bool
	Reason  Reason
	// RetryAfter is how long until the offending window rolls over. Zero when
	// allowed or when the denial is not time-based (e.g. message too large).
	RetryAfter time.Duration
}

// Send describes an outbound attempt being checked.
type Send struct {
	Pubkey     string
	Recipients int
	Bytes      int64
	// NewAccount marks an account younger than Limits.Domain.NewAccountDays.
	NewAccount bool
}

// Limiter enforces outbound limits. It is safe for concurrent use.
type Limiter struct {
	store  Store
	limits Limits
	now    func() time.Time
}

// New creates a Limiter. Pass DefaultLimits() unless the operator overrode them.
func New(store Store, limits Limits) *Limiter {
	return &Limiter{store: store, limits: limits, now: time.Now}
}

// windowKey builds a fixed-window key. All replicas computing the same bucket
// for the same wall-clock window agree, which is what makes this replica-safe.
func windowKey(scope, id, name string, w Window, now time.Time) string {
	bucket := now.Truncate(w.Period).Unix()
	return fmt.Sprintf("rl:%s:%s:%s:%d", scope, id, name, bucket)
}

// resetAfter is the time remaining in the current window.
func resetAfter(w Window, now time.Time) time.Duration {
	return w.Period - now.Sub(now.Truncate(w.Period))
}

// check is one window evaluation: would adding n exceed Max?
type check struct {
	key    string
	window Window
	add    int64
	reason Reason
}

// Allow evaluates every configured window for this send and, if all pass,
// consumes quota from each. Denial consumes nothing — a rejected send must not
// burn the account's budget.
//
// Non-time-based checks (recipients-per-message, message size) are evaluated
// first and short-circuit before any counter is touched.
func (l *Limiter) Allow(ctx context.Context, s Send) (Decision, error) {
	acct := l.limits.Account

	if acct.RecipPerMsg > 0 && s.Recipients > acct.RecipPerMsg {
		return Decision{Reason: ReasonRecipPerMsg}, nil
	}
	if acct.MaxMessageBytes > 0 && s.Bytes > acct.MaxMessageBytes {
		return Decision{Reason: ReasonMessageTooLarge}, nil
	}

	now := l.now()
	recips := int64(s.Recipients)
	if recips < 1 {
		recips = 1
	}

	checks := []check{
		{windowKey("acct", s.Pubkey, "msgmin", acct.MsgPerMin, now), acct.MsgPerMin, 1, ReasonAcctMsgMin},
		{windowKey("acct", s.Pubkey, "msghour", acct.MsgPerHour, now), acct.MsgPerHour, 1, ReasonAcctMsgHour},
		{windowKey("acct", s.Pubkey, "msgday", acct.MsgPerDay, now), acct.MsgPerDay, 1, ReasonAcctMsgDay},
		{windowKey("acct", s.Pubkey, "recipday", acct.RecipPerDay, now), acct.RecipPerDay, recips, ReasonAcctRecipDay},
		{windowKey("domain", "_", "msgmin", l.limits.Domain.MsgPerMin, now), l.limits.Domain.MsgPerMin, 1, ReasonDomainMsgMin},
		{windowKey("domain", "_", "reciphour", l.limits.Domain.RecipPerHour, now), l.limits.Domain.RecipPerHour, recips, ReasonDomainRecipHour},
	}
	if s.NewAccount {
		checks = append(checks, check{
			windowKey("domain", "_new", "recipday", l.limits.Domain.NewAccountRecipPerDay, now),
			l.limits.Domain.NewAccountRecipPerDay, recips, ReasonNewAcctRecipDay,
		})
	}

	// Evaluate all enabled windows without consuming, so a denial by a later
	// window doesn't leave earlier windows incremented.
	var active []check
	keys := make([]string, 0, len(checks))
	for _, c := range checks {
		if c.window.enabled() {
			active = append(active, c)
			keys = append(keys, c.key)
		}
	}
	if len(active) == 0 {
		return Decision{Allowed: true}, nil
	}

	current, err := l.store.PeekAll(ctx, keys)
	if err != nil {
		return Decision{}, fmt.Errorf("ratelimit peek: %w", err)
	}
	for _, c := range active {
		if current[c.key]+c.add > int64(c.window.Max) {
			return Decision{Reason: c.reason, RetryAfter: resetAfter(c.window, now)}, nil
		}
	}

	// All windows pass — consume.
	for _, c := range active {
		if _, err := l.store.IncrBy(ctx, c.key, c.add, c.window.Period); err != nil {
			return Decision{}, fmt.Errorf("ratelimit incr %s: %w", c.reason, err)
		}
	}

	return Decision{Allowed: true}, nil
}
