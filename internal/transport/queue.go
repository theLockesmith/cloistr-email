// Package transport provides email transport mechanisms.
package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// QueuedMessage represents an email message in the outbound queue
type QueuedMessage struct {
	// ID is the unique identifier for this queued message
	ID string `json:"id"`

	// MessageID is the email Message-ID header
	MessageID string `json:"message_id"`

	// From is the envelope sender
	From string `json:"from"`

	// To is the list of envelope recipients
	To []string `json:"to"`

	// RawMessage is the complete RFC 5322 formatted message
	RawMessage []byte `json:"raw_message"`

	// Status is the current queue status
	Status QueueStatus `json:"status"`

	// Attempts is the number of delivery attempts made
	Attempts int `json:"attempts"`

	// MaxAttempts is the maximum number of attempts before giving up
	MaxAttempts int `json:"max_attempts"`

	// LastAttempt is when the last delivery attempt was made
	LastAttempt *time.Time `json:"last_attempt,omitempty"`

	// NextAttempt is when the next delivery attempt should be made
	NextAttempt time.Time `json:"next_attempt"`

	// LastError is the error from the last failed attempt
	LastError string `json:"last_error,omitempty"`

	// CreatedAt is when the message was queued
	CreatedAt time.Time `json:"created_at"`

	// Metadata contains optional metadata about the message
	Metadata map[string]string `json:"metadata,omitempty"`
}

// QueueStatus represents the status of a queued message
type QueueStatus string

const (
	// QueueStatusPending means the message is waiting to be sent
	QueueStatusPending QueueStatus = "pending"

	// QueueStatusProcessing means the message is currently being sent
	QueueStatusProcessing QueueStatus = "processing"

	// QueueStatusSent means the message was successfully delivered
	QueueStatusSent QueueStatus = "sent"

	// QueueStatusFailed means the message permanently failed
	QueueStatusFailed QueueStatus = "failed"

	// QueueStatusRetry means the message failed but will be retried
	QueueStatusRetry QueueStatus = "retry"
)

// OutboundQueue manages the persistent outbound email queue
type OutboundQueue struct {
	db     *sql.DB
	logger *zap.Logger

	// Configuration
	maxAttempts   int
	retryDelays   []time.Duration
	processBatch  int
	pollInterval  time.Duration
}

// OutboundQueueConfig contains configuration for the outbound queue
type OutboundQueueConfig struct {
	// MaxAttempts is the maximum number of delivery attempts (default: 5)
	MaxAttempts int

	// RetryDelays specifies the delay between retry attempts
	// Default: [1m, 5m, 15m, 1h, 4h]
	RetryDelays []time.Duration

	// ProcessBatch is how many messages to process at once (default: 10)
	ProcessBatch int

	// PollInterval is how often to check for pending messages (default: 30s)
	PollInterval time.Duration
}

// DefaultQueueConfig returns sensible defaults for the outbound queue
func DefaultQueueConfig() *OutboundQueueConfig {
	return &OutboundQueueConfig{
		MaxAttempts: 5,
		RetryDelays: []time.Duration{
			1 * time.Minute,
			5 * time.Minute,
			15 * time.Minute,
			1 * time.Hour,
			4 * time.Hour,
		},
		ProcessBatch: 10,
		PollInterval: 30 * time.Second,
	}
}

// NewOutboundQueue creates a new outbound queue
func NewOutboundQueue(db *sql.DB, config *OutboundQueueConfig, logger *zap.Logger) *OutboundQueue {
	if config == nil {
		config = DefaultQueueConfig()
	}

	return &OutboundQueue{
		db:           db,
		logger:       logger,
		maxAttempts:  config.MaxAttempts,
		retryDelays:  config.RetryDelays,
		processBatch: config.ProcessBatch,
		pollInterval: config.PollInterval,
	}
}

// Enqueue adds a message to the outbound queue
func (q *OutboundQueue) Enqueue(ctx context.Context, msg *QueuedMessage) error {
	if msg.ID == "" {
		msg.ID = generateQueueID()
	}
	if msg.MaxAttempts == 0 {
		msg.MaxAttempts = q.maxAttempts
	}
	msg.Status = QueueStatusPending
	msg.CreatedAt = time.Now()
	msg.NextAttempt = time.Now()

	metadataJSON, err := json.Marshal(msg.Metadata)
	if err != nil {
		metadataJSON = []byte("{}")
	}

	toJSON, err := json.Marshal(msg.To)
	if err != nil {
		return fmt.Errorf("failed to marshal recipients: %w", err)
	}

	query := `
		INSERT INTO outbound_queue (
			id, message_id, sender, recipients, raw_message,
			status, attempts, max_attempts, next_attempt, created_at, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err = q.db.ExecContext(ctx, query,
		msg.ID, msg.MessageID, msg.From, toJSON, msg.RawMessage,
		msg.Status, msg.Attempts, msg.MaxAttempts, msg.NextAttempt,
		msg.CreatedAt, metadataJSON,
	)
	if err != nil {
		return fmt.Errorf("failed to enqueue message: %w", err)
	}

	q.logger.Debug("Message enqueued",
		zap.String("id", msg.ID),
		zap.String("message_id", msg.MessageID),
		zap.Int("recipients", len(msg.To)))

	return nil
}

// Dequeue retrieves pending messages ready for delivery
func (q *OutboundQueue) Dequeue(ctx context.Context, limit int) ([]*QueuedMessage, error) {
	if limit <= 0 {
		limit = q.processBatch
	}

	// Select and lock messages that are ready for processing
	query := `
		UPDATE outbound_queue
		SET status = $1, last_attempt = NOW()
		WHERE id IN (
			SELECT id FROM outbound_queue
			WHERE status IN ($2, $3)
			AND next_attempt <= NOW()
			ORDER BY next_attempt ASC
			LIMIT $4
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, message_id, sender, recipients, raw_message,
			status, attempts, max_attempts, last_attempt, next_attempt,
			last_error, created_at, metadata
	`

	rows, err := q.db.QueryContext(ctx, query,
		QueueStatusProcessing, QueueStatusPending, QueueStatusRetry, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to dequeue messages: %w", err)
	}
	defer rows.Close()

	var messages []*QueuedMessage
	for rows.Next() {
		msg := &QueuedMessage{}
		var recipientsJSON, metadataJSON []byte
		var lastAttempt sql.NullTime
		var lastError sql.NullString

		err := rows.Scan(
			&msg.ID, &msg.MessageID, &msg.From, &recipientsJSON, &msg.RawMessage,
			&msg.Status, &msg.Attempts, &msg.MaxAttempts, &lastAttempt, &msg.NextAttempt,
			&lastError, &msg.CreatedAt, &metadataJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan queued message: %w", err)
		}

		if lastAttempt.Valid {
			msg.LastAttempt = &lastAttempt.Time
		}
		if lastError.Valid {
			msg.LastError = lastError.String
		}

		if err := json.Unmarshal(recipientsJSON, &msg.To); err != nil {
			q.logger.Warn("Failed to unmarshal recipients", zap.Error(err))
		}
		if err := json.Unmarshal(metadataJSON, &msg.Metadata); err != nil {
			msg.Metadata = make(map[string]string)
		}

		messages = append(messages, msg)
	}

	return messages, rows.Err()
}

// MarkSent marks a message as successfully sent
func (q *OutboundQueue) MarkSent(ctx context.Context, id string) error {
	query := `
		UPDATE outbound_queue
		SET status = $1, attempts = attempts + 1
		WHERE id = $2
	`

	_, err := q.db.ExecContext(ctx, query, QueueStatusSent, id)
	if err != nil {
		return fmt.Errorf("failed to mark message as sent: %w", err)
	}

	q.logger.Debug("Message marked as sent", zap.String("id", id))
	return nil
}

// MarkFailed marks a message as failed, scheduling a retry if attempts remain
func (q *OutboundQueue) MarkFailed(ctx context.Context, id string, err error) error {
	// First, get the current attempt count
	var attempts, maxAttempts int
	selectQuery := `SELECT attempts, max_attempts FROM outbound_queue WHERE id = $1`
	if selectErr := q.db.QueryRowContext(ctx, selectQuery, id).Scan(&attempts, &maxAttempts); selectErr != nil {
		return fmt.Errorf("failed to get message attempts: %w", selectErr)
	}

	attempts++ // Increment for this attempt

	var status QueueStatus
	var nextAttempt time.Time

	if attempts >= maxAttempts {
		// Permanent failure
		status = QueueStatusFailed
		nextAttempt = time.Now() // Not used, but needs a value
		q.logger.Warn("Message permanently failed",
			zap.String("id", id),
			zap.Int("attempts", attempts),
			zap.Error(err))
	} else {
		// Schedule retry
		status = QueueStatusRetry
		delay := q.getRetryDelay(attempts)
		nextAttempt = time.Now().Add(delay)
		q.logger.Debug("Message scheduled for retry",
			zap.String("id", id),
			zap.Int("attempt", attempts),
			zap.Duration("delay", delay),
			zap.Error(err))
	}

	updateQuery := `
		UPDATE outbound_queue
		SET status = $1, attempts = $2, last_error = $3, next_attempt = $4
		WHERE id = $5
	`

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	_, updateErr := q.db.ExecContext(ctx, updateQuery, status, attempts, errStr, nextAttempt, id)
	if updateErr != nil {
		return fmt.Errorf("failed to mark message as failed: %w", updateErr)
	}

	return nil
}

// getRetryDelay returns the delay for the given attempt number
func (q *OutboundQueue) getRetryDelay(attempt int) time.Duration {
	if attempt <= 0 || len(q.retryDelays) == 0 {
		return 1 * time.Minute
	}

	idx := attempt - 1
	if idx >= len(q.retryDelays) {
		idx = len(q.retryDelays) - 1
	}

	return q.retryDelays[idx]
}

// GetMessage retrieves a specific message from the queue
func (q *OutboundQueue) GetMessage(ctx context.Context, id string) (*QueuedMessage, error) {
	query := `
		SELECT id, message_id, sender, recipients, raw_message,
			status, attempts, max_attempts, last_attempt, next_attempt,
			last_error, created_at, metadata
		FROM outbound_queue
		WHERE id = $1
	`

	msg := &QueuedMessage{}
	var recipientsJSON, metadataJSON []byte
	var lastAttempt sql.NullTime
	var lastError sql.NullString

	err := q.db.QueryRowContext(ctx, query, id).Scan(
		&msg.ID, &msg.MessageID, &msg.From, &recipientsJSON, &msg.RawMessage,
		&msg.Status, &msg.Attempts, &msg.MaxAttempts, &lastAttempt, &msg.NextAttempt,
		&lastError, &msg.CreatedAt, &metadataJSON,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}

	if lastAttempt.Valid {
		msg.LastAttempt = &lastAttempt.Time
	}
	if lastError.Valid {
		msg.LastError = lastError.String
	}

	json.Unmarshal(recipientsJSON, &msg.To)
	json.Unmarshal(metadataJSON, &msg.Metadata)

	return msg, nil
}

// PurgeOld removes old sent and failed messages
func (q *OutboundQueue) PurgeOld(ctx context.Context, olderThan time.Duration) (int64, error) {
	query := `
		DELETE FROM outbound_queue
		WHERE status IN ($1, $2)
		AND created_at < $3
	`

	result, err := q.db.ExecContext(ctx, query,
		QueueStatusSent, QueueStatusFailed, time.Now().Add(-olderThan))
	if err != nil {
		return 0, fmt.Errorf("failed to purge old messages: %w", err)
	}

	count, _ := result.RowsAffected()
	return count, nil
}

// Stats returns queue statistics
func (q *OutboundQueue) Stats(ctx context.Context) (*QueueStats, error) {
	query := `
		SELECT
			COUNT(*) FILTER (WHERE status = $1) as pending,
			COUNT(*) FILTER (WHERE status = $2) as processing,
			COUNT(*) FILTER (WHERE status = $3) as retry,
			COUNT(*) FILTER (WHERE status = $4) as sent,
			COUNT(*) FILTER (WHERE status = $5) as failed
		FROM outbound_queue
	`

	stats := &QueueStats{}
	err := q.db.QueryRowContext(ctx, query,
		QueueStatusPending, QueueStatusProcessing, QueueStatusRetry,
		QueueStatusSent, QueueStatusFailed,
	).Scan(&stats.Pending, &stats.Processing, &stats.Retry, &stats.Sent, &stats.Failed)

	if err != nil {
		return nil, fmt.Errorf("failed to get queue stats: %w", err)
	}

	return stats, nil
}

// QueueStats contains queue statistics
type QueueStats struct {
	Pending    int64 `json:"pending"`
	Processing int64 `json:"processing"`
	Retry      int64 `json:"retry"`
	Sent       int64 `json:"sent"`
	Failed     int64 `json:"failed"`
}

// generateQueueID generates a unique queue message ID
func generateQueueID() string {
	return fmt.Sprintf("q_%d_%s", time.Now().UnixNano(), randomString(8))
}
