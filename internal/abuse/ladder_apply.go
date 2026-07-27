package abuse

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// SendStateStore is the email-local send gate the ladder flips for the hold
// rung. Satisfied by *storage.PostgreSQL.
type SendStateStore interface {
	SetMailboxSendEnabled(ctx context.Context, pubkey string, enabled bool, suspendedAt *time.Time) error
}

// QueueHolder parks and releases an account's queued outbound mail. Satisfied by
// *transport.OutboundQueue.
//
// Holding rather than rejecting is the whole point: a held sender's mail sits in
// the queue and goes out intact if the hold turns out to be wrong.
type QueueHolder interface {
	HoldForSender(ctx context.Context, senderPubkey string) (int64, error)
	ReleaseForSender(ctx context.Context, senderPubkey string) (int64, error)
}

// Config tunes how long each rung persists.
type Config struct {
	Thresholds Thresholds

	// ThrottleTTL is how long a throttle mark lasts before expiring on its own.
	ThrottleTTL time.Duration

	// WarnTTL is how long a warn mark lasts.
	WarnTTL time.Duration

	// AutoReleaseHold lets the ladder lift a hold once the account's signals
	// come back clean. Velocity holds in particular are often transient — a
	// legitimate burst — and requiring a human for every one of them makes the
	// tripwire too expensive to leave armed.
	AutoReleaseHold bool

	// MinHoldDuration is the floor before an auto-release can fire, so an
	// account cannot oscillate in and out of hold within a single scan window.
	MinHoldDuration time.Duration
}

// DefaultConfig returns the ladder defaults.
func DefaultConfig() Config {
	return Config{
		Thresholds:      DefaultThresholds(),
		ThrottleTTL:     6 * time.Hour,
		WarnTTL:         24 * time.Hour,
		AutoReleaseHold: true,
		MinHoldDuration: 1 * time.Hour,
	}
}

// Ladder applies rungs to accounts. It is safe for concurrent use.
type Ladder struct {
	cfg    Config
	marks  MarkStore
	state  SendStateStore
	queue  QueueHolder
	logger *zap.Logger
	now    func() time.Time
}

// NewLadder builds a ladder. queue may be nil, in which case the hold rung still
// blocks new sends but does not park already-queued mail.
func NewLadder(cfg Config, marks MarkStore, state SendStateStore, queue QueueHolder, logger *zap.Logger) *Ladder {
	return &Ladder{cfg: cfg, marks: marks, state: state, queue: queue, logger: logger, now: time.Now}
}

// Apply moves an account to the rung its signals warrant, escalating or
// de-escalating from wherever it currently sits. It reports the rung actually
// applied, which can be lower than warranted when policy clamps it.
func (l *Ladder) Apply(ctx context.Context, pubkey string, warranted Rung, reason string) (Rung, error) {
	current, hasMark, err := l.marks.Get(ctx, pubkey)
	if err != nil {
		// An unreadable mark means the ladder cannot tell escalation from
		// de-escalation. Treat it as unmarked and re-apply: re-applying a rung
		// is idempotent, whereas skipping leaves an abuser unenforced.
		l.logger.Warn("Failed to read abuse mark; treating account as unmarked",
			zap.String("pubkey", pubkey), zap.Error(err))
		current, hasMark = Mark{}, false
	}

	applied := warranted
	if applied == RungSuspend && !l.cfg.Thresholds.AllowAutoSuspend {
		// Clamp, but keep the warranted level in the log — the whole point of
		// recording it is that an operator can find the accounts that earned a
		// suspend and decide by hand.
		applied = RungHold
		l.logger.Warn("Suspend warranted but auto-suspend is disabled; holding instead",
			zap.String("pubkey", pubkey),
			zap.String("reason", reason))
	}

	switch {
	case hasMark && applied < current.Rung:
		return l.deescalate(ctx, pubkey, current, applied)

	case applied == RungNone:
		return RungNone, nil

	case hasMark && applied == current.Rung:
		// Still warranted at the same level. This is a renewal, not a
		// re-escalation: re-running enforcement here would release-then-re-hold
		// through the de-escalation path and briefly re-open the gate.
		return applied, l.renew(ctx, pubkey, current, reason)

	default:
		return applied, l.escalate(ctx, pubkey, applied, reason)
	}
}

// renew refreshes a mark that is still warranted at its current rung.
//
// The original At is preserved so that MinHoldDuration measures from when the
// hold actually started. Stamping it forward on every scan would make a hold
// permanently ineligible for auto-release.
func (l *Ladder) renew(ctx context.Context, pubkey string, current Mark, reason string) error {
	renewed := current
	if reason != "" {
		renewed.Reason = reason
	}

	if err := l.marks.Set(ctx, pubkey, renewed, l.rungTTL(current.Rung)); err != nil {
		return fmt.Errorf("renew abuse mark: %w", err)
	}
	return nil
}

// escalate applies the enforcement for a rung and records the mark.
func (l *Ladder) escalate(ctx context.Context, pubkey string, rung Rung, reason string) error {
	now := l.now()

	switch rung {
	case RungHold, RungSuspend:
		if err := l.state.SetMailboxSendEnabled(ctx, pubkey, false, &now); err != nil {
			return fmt.Errorf("hold send state: %w", err)
		}
		if l.queue != nil {
			held, err := l.queue.HoldForSender(ctx, pubkey)
			if err != nil {
				// The send gate is already closed, so new mail is stopped. Failing
				// to park the backlog is worth shouting about but not worth
				// abandoning the hold.
				l.logger.Error("Held account but failed to park queued mail",
					zap.String("pubkey", pubkey), zap.Error(err))
			} else if held > 0 {
				l.logger.Info("Parked queued mail for held account",
					zap.String("pubkey", pubkey), zap.Int64("messages", held))
			}
		}
	}

	ttl := l.rungTTL(rung)
	mark := Mark{Rung: rung, Reason: reason, At: now}
	if err := l.marks.Set(ctx, pubkey, mark, ttl); err != nil {
		return fmt.Errorf("record abuse mark: %w", err)
	}

	l.logger.Warn("Abuse ladder escalated account",
		zap.String("pubkey", pubkey),
		zap.String("rung", rung.String()),
		zap.String("reason", reason),
		zap.Duration("ttl", ttl))

	return nil
}

// deescalate lifts enforcement when an account's signals have come back clean.
func (l *Ladder) deescalate(ctx context.Context, pubkey string, current Mark, warranted Rung) (Rung, error) {
	// Suspend is never lifted automatically. Reaching it took a deliberate
	// operator decision, and undoing it should too.
	if current.Rung >= RungSuspend {
		return current.Rung, nil
	}

	if current.Rung >= RungHold {
		if !l.cfg.AutoReleaseHold {
			return current.Rung, nil
		}
		if l.now().Sub(current.At) < l.cfg.MinHoldDuration {
			// Too soon: releasing now would let a burst resume before the
			// signals that triggered the hold have had time to settle.
			return current.Rung, nil
		}
		if err := l.release(ctx, pubkey); err != nil {
			return current.Rung, err
		}
	}

	if warranted == RungNone {
		if err := l.marks.Clear(ctx, pubkey); err != nil {
			return current.Rung, fmt.Errorf("clear abuse mark: %w", err)
		}
		l.logger.Info("Abuse ladder cleared account",
			zap.String("pubkey", pubkey),
			zap.String("from_rung", current.Rung.String()))
		return RungNone, nil
	}

	// Still warrants something, just less. Re-mark at the lower rung so the TTL
	// restarts from the current, milder judgement.
	mark := Mark{Rung: warranted, Reason: current.Reason, At: l.now()}
	if err := l.marks.Set(ctx, pubkey, mark, l.rungTTL(warranted)); err != nil {
		return current.Rung, fmt.Errorf("record abuse mark: %w", err)
	}

	l.logger.Info("Abuse ladder de-escalated account",
		zap.String("pubkey", pubkey),
		zap.String("from_rung", current.Rung.String()),
		zap.String("to_rung", warranted.String()))

	return warranted, nil
}

// release re-opens the send gate and unparks the account's queued mail.
func (l *Ladder) release(ctx context.Context, pubkey string) error {
	if err := l.state.SetMailboxSendEnabled(ctx, pubkey, true, nil); err != nil {
		return fmt.Errorf("release send state: %w", err)
	}
	if l.queue != nil {
		released, err := l.queue.ReleaseForSender(ctx, pubkey)
		if err != nil {
			// The gate is open again but the backlog is still parked. Surface it
			// loudly: the user's mail is sitting in the queue, undelivered.
			l.logger.Error("Released account but failed to unpark queued mail",
				zap.String("pubkey", pubkey), zap.Error(err))
		} else if released > 0 {
			l.logger.Info("Unparked queued mail for released account",
				zap.String("pubkey", pubkey), zap.Int64("messages", released))
		}
	}
	return nil
}

// rungTTL is how long a mark at this rung survives without being renewed.
// Hold and suspend do not expire: they gate on Postgres state that a scan or an
// operator must explicitly undo.
func (l *Ladder) rungTTL(rung Rung) time.Duration {
	switch rung {
	case RungWarn:
		return l.cfg.WarnTTL
	case RungThrottle:
		return l.cfg.ThrottleTTL
	default:
		return 0
	}
}

// ReleaseManual lifts any hold on an account and clears its mark, for operator
// use. It works regardless of AutoReleaseHold or MinHoldDuration, and is the
// only supported way out of a suspend.
func (l *Ladder) ReleaseManual(ctx context.Context, pubkey string) error {
	if err := l.release(ctx, pubkey); err != nil {
		return err
	}
	if err := l.marks.Clear(ctx, pubkey); err != nil {
		return fmt.Errorf("clear abuse mark: %w", err)
	}

	l.logger.Info("Abuse ladder mark manually released", zap.String("pubkey", pubkey))
	return nil
}
