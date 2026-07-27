// Package abuse implements the outbound detection→suspend ladder.
//
// The send gate (internal/email) answers "may this account send right now?"
// from tier and rate limits. It cannot answer "is this account behaving like a
// spammer?", because that only shows up across many sends: a high bounce rate
// means the sender is mailing addresses it never legitimately collected, and a
// velocity spike means a compromised or throwaway account is burning its budget
// as fast as the limiter allows.
//
// This package watches those aggregates and escalates through four rungs —
// warn, throttle, hold, suspend — so that a false positive costs a legitimate
// sender some latency rather than their account. Everything is keyed on the
// sender's Nostr pubkey, never IP, for the same privacy reason as the limiter.
package abuse

import (
	"fmt"
	"time"
)

// Rung is a severity level on the ladder. Higher is more restrictive.
type Rung int

const (
	// RungNone is normal operation; nothing is applied.
	RungNone Rung = iota

	// RungWarn records the account as suspect and alerts operators. No
	// enforcement — this rung exists so that a human sees a pattern before it
	// costs anyone a send.
	RungWarn

	// RungThrottle clamps the account to a fraction of its normal limits. A
	// legitimate sender having a bad day slows down; a spammer's throughput
	// collapses.
	RungThrottle

	// RungHold stops outbound entirely and parks queued mail. Held mail is
	// retained, not bounced, so a wrongly-held sender loses nothing but time.
	RungHold

	// RungSuspend is the platform-wide hammer. It is deliberately hard to reach
	// and, by default, not applied automatically — see Thresholds.AllowAutoSuspend.
	RungSuspend
)

// String renders the rung for logs and audit records.
func (r Rung) String() string {
	switch r {
	case RungNone:
		return "none"
	case RungWarn:
		return "warn"
	case RungThrottle:
		return "throttle"
	case RungHold:
		return "hold"
	case RungSuspend:
		return "suspend"
	default:
		return fmt.Sprintf("rung(%d)", int(r))
	}
}

// Signals are the aggregate behaviours the ladder judges an account by, all
// measured over Thresholds.Window except RecipientsLastHour.
type Signals struct {
	// Pubkey identifies the sending account.
	Pubkey string

	// MessagesSent is how many messages the account submitted in the window.
	// This is the denominator for every rate below.
	MessagesSent int

	// RecipientsSent is the total recipient count across those messages.
	RecipientsSent int

	// HardBounces are permanent failures — the strongest signal, because they
	// mean the sender is mailing addresses that do not exist.
	HardBounces int

	// SoftBounces are temporary failures. Weak signal on their own: a full
	// mailbox says nothing about the sender.
	SoftBounces int

	// RecipientsLastHour measures burst velocity, which catches a compromised
	// account before a 24h bounce rate has time to move.
	RecipientsLastHour int

	// Complaints are feedback-loop reports: a real recipient pressed "report
	// spam". Stronger evidence than a bounce, which only says an address was
	// wrong. Always zero until the sending domains are FBL-enrolled.
	Complaints int
}

// HardBounceRate is hard bounces per message sent, in [0,1]. Zero when the
// account sent nothing, so an idle account can never be judged.
func (s Signals) HardBounceRate() float64 {
	if s.MessagesSent <= 0 {
		return 0
	}
	return float64(s.HardBounces) / float64(s.MessagesSent)
}

// ComplaintRate is complaints per recipient reached, in [0,1].
//
// The denominator is recipients rather than messages: a complaint comes from one
// person, so a single message to 500 people that draws 5 complaints is a 1%
// complaint rate, not 500%.
func (s Signals) ComplaintRate() float64 {
	if s.RecipientsSent <= 0 {
		return 0
	}
	return float64(s.Complaints) / float64(s.RecipientsSent)
}

// Thresholds is the operator-configurable trigger set. Every rate is a fraction
// of messages sent, not an absolute count, so a high-volume legitimate sender is
// not punished for scale.
type Thresholds struct {
	// Window is how far back the aggregates look.
	Window time.Duration

	// MinMessages is the volume floor. Below it, rates are statistical noise —
	// one bounce out of three messages is a 33% rate and means nothing — so no
	// rate-based rung fires at all.
	MinMessages int

	// WarnHardBounceRate and friends are the escalation points, evaluated
	// highest-first.
	WarnHardBounceRate     float64
	ThrottleHardBounceRate float64
	HoldHardBounceRate     float64
	SuspendHardBounceRate  float64

	// Complaint-rate escalation points. These are an order of magnitude tighter
	// than the bounce thresholds because they measure something stronger: a real
	// recipient reporting the mail as spam, not merely a wrong address. Large
	// providers begin throttling a sender around 0.1%.
	WarnComplaintRate     float64
	ThrottleComplaintRate float64
	HoldComplaintRate     float64

	// MinRecipientsForComplaintRate is the volume floor for complaint rate,
	// counted in recipients, since that is the denominator.
	MinRecipientsForComplaintRate int

	// VelocityRecipientsPerHour holds an account outright when exceeded,
	// regardless of bounce rate. This is the compromised-account tripwire: it
	// fires before any bounce has had time to come back.
	VelocityRecipientsPerHour int

	// AllowAutoSuspend gates the top rung. Default false, because on this
	// platform a suspend sets users.enabled = FALSE, which revokes access to
	// every Cloistr service — drive, sheets, relay — not just email. Losing your
	// documents because your mail bounced is not a proportionate response, so by
	// default the ladder tops out at hold and asks a human to finish the job.
	AllowAutoSuspend bool
}

// DefaultThresholds returns conservative starting values.
//
// They are deliberately loose: this ladder acts without a human in the loop, so
// the cost of a false positive is paid by a real user. Tighten them once there
// is enough observed traffic to know what a normal bounce rate looks like here.
func DefaultThresholds() Thresholds {
	return Thresholds{
		Window:                    24 * time.Hour,
		MinMessages:               20,
		WarnHardBounceRate:        0.05,
		ThrottleHardBounceRate:    0.15,
		HoldHardBounceRate:        0.35,
		SuspendHardBounceRate:     0.60,
		VelocityRecipientsPerHour: 500,
		AllowAutoSuspend:          false,

		WarnComplaintRate:             0.001,
		ThrottleComplaintRate:         0.003,
		HoldComplaintRate:             0.005,
		MinRecipientsForComplaintRate: 200,
	}
}

// Evaluate maps signals to the rung they warrant. It is pure: every decision the
// ladder makes is reproducible from the numbers alone, which is what makes an
// automated enforcement action auditable after the fact.
//
// The returned Rung is what the signals *warrant*, before AllowAutoSuspend is
// applied — see Ladder.Apply, which is where the policy clamp lives, so that
// logs can record "warranted suspend, applied hold".
func (t Thresholds) Evaluate(s Signals) (Rung, string) {
	// Velocity first: it is the only signal that works on an account with no
	// bounce history yet, which is exactly the compromised-account case.
	if t.VelocityRecipientsPerHour > 0 && s.RecipientsLastHour > t.VelocityRecipientsPerHour {
		return RungHold, fmt.Sprintf("velocity %d recipients/hour exceeds %d",
			s.RecipientsLastHour, t.VelocityRecipientsPerHour)
	}

	// Each remaining signal proposes a rung; the account lands on the highest.
	// Signals must not cancel each other out — a clean bounce rate says nothing
	// about whether recipients are reporting the mail as spam.
	rung, reason := t.evaluateBounceRate(s)

	if cRung, cReason := t.evaluateComplaintRate(s); cRung > rung {
		rung, reason = cRung, cReason
	}

	return rung, reason
}

// evaluateBounceRate judges the account on hard bounces per message.
func (t Thresholds) evaluateBounceRate(s Signals) (Rung, string) {
	// Below the volume floor, rates carry no information.
	if s.MessagesSent < t.MinMessages {
		return RungNone, ""
	}

	rate := s.HardBounceRate()
	reason := func(threshold float64) string {
		return fmt.Sprintf("hard bounce rate %.1f%% (%d/%d) exceeds %.1f%%",
			rate*100, s.HardBounces, s.MessagesSent, threshold*100)
	}

	switch {
	case t.SuspendHardBounceRate > 0 && rate >= t.SuspendHardBounceRate:
		return RungSuspend, reason(t.SuspendHardBounceRate)
	case t.HoldHardBounceRate > 0 && rate >= t.HoldHardBounceRate:
		return RungHold, reason(t.HoldHardBounceRate)
	case t.ThrottleHardBounceRate > 0 && rate >= t.ThrottleHardBounceRate:
		return RungThrottle, reason(t.ThrottleHardBounceRate)
	case t.WarnHardBounceRate > 0 && rate >= t.WarnHardBounceRate:
		return RungWarn, reason(t.WarnHardBounceRate)
	}

	return RungNone, ""
}

// evaluateComplaintRate judges the account on feedback-loop complaints per
// recipient. It tops out at hold: a complaint rate says the mail was unwanted,
// which is not the same as fraud, and the platform-wide hammer should not fall
// on evidence this indirect.
func (t Thresholds) evaluateComplaintRate(s Signals) (Rung, string) {
	if s.RecipientsSent < t.MinRecipientsForComplaintRate {
		return RungNone, ""
	}

	rate := s.ComplaintRate()
	reason := func(threshold float64) string {
		return fmt.Sprintf("spam complaint rate %.2f%% (%d/%d) exceeds %.2f%%",
			rate*100, s.Complaints, s.RecipientsSent, threshold*100)
	}

	switch {
	case t.HoldComplaintRate > 0 && rate >= t.HoldComplaintRate:
		return RungHold, reason(t.HoldComplaintRate)
	case t.ThrottleComplaintRate > 0 && rate >= t.ThrottleComplaintRate:
		return RungThrottle, reason(t.ThrottleComplaintRate)
	case t.WarnComplaintRate > 0 && rate >= t.WarnComplaintRate:
		return RungWarn, reason(t.WarnComplaintRate)
	}

	return RungNone, ""
}
