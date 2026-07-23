// Package ratelimit provides the outbound SMTP rate limiter for cloistr-email.
//
// Everything is keyed on the sender's Nostr pubkey — never IP — which is a hard
// privacy constraint (a user's messages must not be correlatable by network
// address). Limits are enforced as fixed-window counters in Redis/Dragonfly so
// they hold across all backend replicas; an in-memory Store backs tests.
//
// Two dimensions are limited: message count and recipient count (recipients are
// the real spam metric). Both per-account and domain-wide. Every value is
// operator-configurable: a global default, overridable per account, and a
// self-hoster can raise or disable any of them.
package ratelimit

import "time"

// Window is a single fixed-window limit: at most Max events per Period.
// Max <= 0 disables the window (unlimited).
type Window struct {
	Max    int
	Period time.Duration
}

func (w Window) enabled() bool { return w.Max > 0 }

// AccountLimits are the per-account outbound limits. Zero-value Max disables a
// given window (used for elevated/operator accounts).
type AccountLimits struct {
	MsgPerMin       Window // messages per minute
	MsgPerHour      Window // messages per hour
	MsgPerDay       Window // messages per day
	RecipPerMsg     int    // max recipients in a single message (0 = unlimited)
	RecipPerDay     Window // recipients per day (the real spam metric)
	MaxMessageBytes int64  // max serialized message size (0 = unlimited)
}

// DomainLimits are the aggregate limits across ALL accounts on the domain — a
// backstop against many-account abuse and a warmup guard.
type DomainLimits struct {
	MsgPerMin      Window // total messages per minute across the domain
	RecipPerHour   Window // total recipients per hour across the domain
	NewAccountDays int    // accounts younger than this are "new" (sub-capped)
	NewAccountRecipPerDay Window // aggregate recipients/day cap for all new accounts
}

// Limits is the full operator-configurable limit set.
type Limits struct {
	Account AccountLimits
	Domain  DomainLimits
}

// DefaultLimits returns the handoff-spec defaults (audit C).
//
//	Per-account: 5 msg/min, 100/hr, 500/day, 50 recipients/msg,
//	             1000 recipients/day, 25 MB max.
//	Domain-wide: 300 msg/min, 5000 recipients/hr, new-account (<7d) sub-cap.
func DefaultLimits() Limits {
	return Limits{
		Account: AccountLimits{
			MsgPerMin:       Window{Max: 5, Period: time.Minute},
			MsgPerHour:      Window{Max: 100, Period: time.Hour},
			MsgPerDay:       Window{Max: 500, Period: 24 * time.Hour},
			RecipPerMsg:     50,
			RecipPerDay:     Window{Max: 1000, Period: 24 * time.Hour},
			MaxMessageBytes: 25 * 1024 * 1024,
		},
		Domain: DomainLimits{
			MsgPerMin:             Window{Max: 300, Period: time.Minute},
			RecipPerHour:          Window{Max: 5000, Period: time.Hour},
			NewAccountDays:        7,
			NewAccountRecipPerDay: Window{Max: 200, Period: 24 * time.Hour},
		},
	}
}

// Elevated returns limits for paid / WoT-vouched accounts: the per-account
// message/recipient windows are lifted, but the message-size cap and the
// domain-wide backstops still apply.
func (l Limits) Elevated() Limits {
	e := l
	e.Account.MsgPerMin = Window{}
	e.Account.MsgPerHour = Window{}
	e.Account.MsgPerDay = Window{}
	e.Account.RecipPerMsg = 0
	e.Account.RecipPerDay = Window{}
	return e
}

// NewAccountRecipPerDay returns the per-account recipient/day cap for a "new"
// (<NewAccountDays) named account: 20 recipients/day per the send-rights table.
func NewAccountAccountLimits(base AccountLimits) AccountLimits {
	base.RecipPerDay = Window{Max: 20, Period: 24 * time.Hour}
	return base
}
