// Package usage reconciles cloistr-email's share of the shared storage quota.
//
// Storage is a single platform-wide pool: blossom, drive, photos, tasks and
// email each report a component into user_quota_usage, and a user's total is the
// SUM across services. There is no separate email quota.
//
// Reporting incrementally on every write would be cheaper, but it drifts —
// deletes, failed sends, and rows removed by cascade all leak. This package
// instead measures what the mailbox actually holds and corrects the recorded
// component to match, so drift is bounded by the reconcile interval rather than
// accumulating forever.
package usage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-common/platform"
	"go.uber.org/zap"
)

// serviceID is this service's component key in user_quota_usage.
const serviceID = "email"

// Recorder applies a usage delta. Satisfied by *platform.Client.
//
// The platform API is deliberately additive — services normally report "I just
// stored 4 KB" — so the reconciler converts its absolute measurement into a
// delta against what is currently recorded.
type Recorder interface {
	RecordUsage(ctx context.Context, pubkey string, quotaType string, amount int64) error
}

// Reconciler measures per-mailbox storage and corrects the recorded component.
type Reconciler struct {
	db       *sql.DB
	recorder Recorder
	logger   *zap.Logger
}

// New builds a reconciler over the email database and a platform client.
func New(db *sql.DB, recorder Recorder, logger *zap.Logger) *Reconciler {
	return &Reconciler{db: db, recorder: recorder, logger: logger}
}

// Result summarises one reconcile pass.
type Result struct {
	// Mailboxes is how many mailboxes were measured.
	Mailboxes int

	// Corrected is how many had a component that disagreed with reality.
	Corrected int

	// NetBytes is the total correction applied, signed. A large negative value
	// means deletes had been leaking quota.
	NetBytes int64
}

// Reconcile measures every mailbox's stored bytes and corrects its recorded
// component to match.
//
// A failure on one mailbox does not abort the pass: one user's bad row must not
// leave everyone else's quota stale.
func (r *Reconciler) Reconcile(ctx context.Context) (Result, error) {
	var result Result

	measured, err := r.measure(ctx)
	if err != nil {
		return result, err
	}

	recorded, err := r.recordedComponents(ctx)
	if err != nil {
		return result, err
	}

	// Mailboxes that once had data but now hold none still need correcting down
	// to zero, so walk the union of both sides rather than just what was measured.
	for pubkey := range recorded {
		if _, ok := measured[pubkey]; !ok {
			measured[pubkey] = 0
		}
	}

	for pubkey, actual := range measured {
		result.Mailboxes++

		delta := actual - recorded[pubkey]
		if delta == 0 {
			continue
		}

		if err := r.recorder.RecordUsage(ctx, pubkey, platform.QuotaTypeStorageBytes, delta); err != nil {
			r.logger.Error("Failed to correct recorded storage usage",
				zap.String("pubkey", pubkey),
				zap.Int64("delta", delta),
				zap.Error(err))
			continue
		}

		result.Corrected++
		result.NetBytes += delta

		r.logger.Debug("Corrected recorded storage usage",
			zap.String("pubkey", pubkey),
			zap.Int64("measured", actual),
			zap.Int64("delta", delta))
	}

	return result, nil
}

// measure returns actual stored bytes per mailbox.
func (r *Reconciler) measure(ctx context.Context) (map[string]int64, error) {
	bytes, err := r.measureMessages(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.addInlineAttachments(ctx, bytes); err != nil {
		return nil, err
	}
	return bytes, nil
}

// measureMessages sums the message content this service stores in its own
// database. octet_length rather than length: the quota is in bytes, and a
// character count would under-report every non-ASCII message.
func (r *Reconciler) measureMessages(ctx context.Context) (map[string]int64, error) {
	const query = `
		SELECT
			mailbox_pubkey,
			COALESCE(SUM(
				octet_length(COALESCE(subject, '')) +
				octet_length(COALESCE(body, '')) +
				octet_length(COALESCE(html_body, ''))
			), 0)
		FROM emails
		WHERE deleted_at IS NULL
		GROUP BY mailbox_pubkey
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("measure message storage: %w", err)
	}
	defer rows.Close()

	bytes := make(map[string]int64)
	for rows.Next() {
		var pubkey string
		var n int64
		if err := rows.Scan(&pubkey, &n); err != nil {
			return nil, fmt.Errorf("scan message storage: %w", err)
		}
		bytes[pubkey] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate message storage: %w", err)
	}

	return bytes, nil
}

// addInlineAttachments folds in attachments still stored locally.
//
// Blossom-offloaded attachments are excluded: the blob lives on a Blossom server
// and is already reported under the blossom component. Counting it here too
// would charge the user twice for one file out of a shared pool.
func (r *Reconciler) addInlineAttachments(ctx context.Context, bytes map[string]int64) error {
	const query = `
		SELECT e.mailbox_pubkey, COALESCE(SUM(a.size_bytes), 0)
		FROM attachments a
		JOIN emails e ON e.id = a.email_id
		WHERE a.blossom_sha256 IS NULL AND e.deleted_at IS NULL
		GROUP BY e.mailbox_pubkey
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("measure attachment storage: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var pubkey string
		var n int64
		if err := rows.Scan(&pubkey, &n); err != nil {
			return fmt.Errorf("scan attachment storage: %w", err)
		}
		bytes[pubkey] += n
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate attachment storage: %w", err)
	}

	return nil
}

// recordedComponents reads back what this service currently has on record.
//
// Reading the platform's own number rather than a locally cached one is what
// makes this a reconciler: it converges even if a previous pass died halfway or
// something else reset the row.
func (r *Reconciler) recordedComponents(ctx context.Context) (map[string]int64, error) {
	const query = `
		SELECT pubkey, bytes
		FROM user_quota_usage
		WHERE quota_type_id = $1 AND service = $2
	`

	rows, err := r.db.QueryContext(ctx, query, platform.QuotaTypeStorageBytes, serviceID)
	if err != nil {
		// Standalone deployments have no platform quota tables. Treat every
		// component as zero; RecordUsage falls through to in-memory tracking.
		if strings.Contains(err.Error(), "does not exist") {
			return map[string]int64{}, nil
		}
		return nil, fmt.Errorf("read recorded usage: %w", err)
	}
	defer rows.Close()

	recorded := make(map[string]int64)
	for rows.Next() {
		var pubkey string
		var n int64
		if err := rows.Scan(&pubkey, &n); err != nil {
			return nil, fmt.Errorf("scan recorded usage: %w", err)
		}
		recorded[pubkey] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recorded usage: %w", err)
	}

	return recorded, nil
}

// Run reconciles every interval until ctx is cancelled, starting with an
// immediate pass so a fresh deployment does not wait a full interval before its
// quota numbers mean anything.
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) {
	r.logger.Info("Storage usage reconciler started", zap.Duration("interval", interval))

	r.runOnce(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("Storage usage reconciler stopped")
			return
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

func (r *Reconciler) runOnce(ctx context.Context) {
	result, err := r.Reconcile(ctx)
	if err != nil {
		r.logger.Error("Storage usage reconcile failed", zap.Error(err))
		return
	}

	if result.Corrected > 0 {
		r.logger.Info("Storage usage reconciled",
			zap.Int("mailboxes", result.Mailboxes),
			zap.Int("corrected", result.Corrected),
			zap.Int64("net_bytes", result.NetBytes))
	} else {
		r.logger.Debug("Storage usage already in sync",
			zap.Int("mailboxes", result.Mailboxes))
	}
}
