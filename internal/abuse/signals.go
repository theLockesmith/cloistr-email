package abuse

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SignalSource gathers the behavioural aggregates the ladder judges accounts by.
type SignalSource interface {
	// Collect returns signals for every account that sent mail inside window.
	// Accounts that sent nothing are absent: an idle account cannot be abusive,
	// and scanning every account on the platform would not scale.
	Collect(ctx context.Context, window time.Duration) ([]Signals, error)
}

// PostgresSignals derives signals from the outbound queue and bounce log.
type PostgresSignals struct{ db *sql.DB }

// NewPostgresSignals wraps a database handle.
func NewPostgresSignals(db *sql.DB) *PostgresSignals { return &PostgresSignals{db: db} }

// Collect runs two aggregate queries — sends and bounces — and joins them in
// process. Two queries regardless of how many accounts are active, so the scan
// cost does not grow with the user base.
//
// Note the denominator's shelf life: outbound_queue rows are purged once
// delivered and aged out, so window must stay well inside the purge horizon or
// the send count will under-report and inflate every rate.
func (p *PostgresSignals) Collect(ctx context.Context, window time.Duration) ([]Signals, error) {
	since := time.Now().Add(-window)
	hourAgo := time.Now().Add(-time.Hour)

	byPubkey, err := p.collectSends(ctx, since, hourAgo)
	if err != nil {
		return nil, err
	}
	if len(byPubkey) == 0 {
		return nil, nil
	}

	if err := p.applyBounces(ctx, since, byPubkey); err != nil {
		return nil, err
	}

	if err := p.applyComplaints(ctx, since, byPubkey); err != nil {
		return nil, err
	}

	out := make([]Signals, 0, len(byPubkey))
	for _, s := range byPubkey {
		out = append(out, *s)
	}
	return out, nil
}

// collectSends aggregates per-account outbound volume, including the last-hour
// recipient count that drives the velocity tripwire.
func (p *PostgresSignals) collectSends(ctx context.Context, since, hourAgo time.Time) (map[string]*Signals, error) {
	const query = `
		SELECT
			metadata->>'sender_pubkey' AS pubkey,
			COUNT(*) AS messages,
			COALESCE(SUM(jsonb_array_length(recipients)), 0) AS recipients,
			COALESCE(SUM(jsonb_array_length(recipients)) FILTER (WHERE created_at > $2), 0) AS recipients_last_hour
		FROM outbound_queue
		WHERE created_at > $1 AND metadata->>'sender_pubkey' IS NOT NULL
		GROUP BY 1
	`

	rows, err := p.db.QueryContext(ctx, query, since, hourAgo)
	if err != nil {
		if isMissingRelation(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("collect send signals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byPubkey := make(map[string]*Signals)
	for rows.Next() {
		s := &Signals{}
		if err := rows.Scan(&s.Pubkey, &s.MessagesSent, &s.RecipientsSent, &s.RecipientsLastHour); err != nil {
			return nil, fmt.Errorf("scan send signals: %w", err)
		}
		byPubkey[s.Pubkey] = s
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate send signals: %w", err)
	}

	return byPubkey, nil
}

// applyBounces folds per-account bounce counts into already-collected signals.
// Bounces from accounts with no sends in the window are ignored: without a
// denominator there is no rate to judge.
func (p *PostgresSignals) applyBounces(ctx context.Context, since time.Time, byPubkey map[string]*Signals) error {
	const query = `
		SELECT
			sender_pubkey,
			COUNT(*) FILTER (WHERE bounce_type = 'hard') AS hard,
			COUNT(*) FILTER (WHERE bounce_type = 'soft') AS soft
		FROM email_bounces
		WHERE received_at > $1 AND sender_pubkey IS NOT NULL
		GROUP BY 1
	`

	rows, err := p.db.QueryContext(ctx, query, since)
	if err != nil {
		if isMissingRelation(err) {
			return nil
		}
		return fmt.Errorf("collect bounce signals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var pubkey string
		var hard, soft int
		if err := rows.Scan(&pubkey, &hard, &soft); err != nil {
			return fmt.Errorf("scan bounce signals: %w", err)
		}
		if s, ok := byPubkey[pubkey]; ok {
			s.HardBounces, s.SoftBounces = hard, soft
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate bounce signals: %w", err)
	}

	return nil
}

// applyComplaints folds in feedback-loop complaints. Yields nothing until the
// sending domains are FBL-enrolled, and nothing at all on a deployment that has
// not run migration 010.
func (p *PostgresSignals) applyComplaints(ctx context.Context, since time.Time, byPubkey map[string]*Signals) error {
	const query = `
		SELECT sender_pubkey, COUNT(*)
		FROM email_complaints
		WHERE received_at > $1 AND sender_pubkey IS NOT NULL
		GROUP BY 1
	`

	rows, err := p.db.QueryContext(ctx, query, since)
	if err != nil {
		if isMissingRelation(err) {
			return nil
		}
		return fmt.Errorf("collect complaint signals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var pubkey string
		var complaints int
		if err := rows.Scan(&pubkey, &complaints); err != nil {
			return fmt.Errorf("scan complaint signals: %w", err)
		}
		if s, ok := byPubkey[pubkey]; ok {
			s.Complaints = complaints
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate complaint signals: %w", err)
	}

	return nil
}

// isMissingRelation reports whether a query failed only because an optional
// table has not been migrated yet, which self-hosters can legitimately hit.
func isMissingRelation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "does not exist")
}
