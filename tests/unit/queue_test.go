package unit

import (
	"testing"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDefaultQueueConfig(t *testing.T) {
	config := transport.DefaultQueueConfig()

	assert.Equal(t, 5, config.MaxAttempts)
	assert.Equal(t, 10, config.ProcessBatch)
	assert.Equal(t, 30*time.Second, config.PollInterval)
	require.Len(t, config.RetryDelays, 5)
	assert.Equal(t, 1*time.Minute, config.RetryDelays[0])
	assert.Equal(t, 5*time.Minute, config.RetryDelays[1])
	assert.Equal(t, 15*time.Minute, config.RetryDelays[2])
	assert.Equal(t, 1*time.Hour, config.RetryDelays[3])
	assert.Equal(t, 4*time.Hour, config.RetryDelays[4])
}

func TestNewOutboundQueue(t *testing.T) {
	logger := zap.NewNop()

	t.Run("with nil config uses defaults", func(t *testing.T) {
		queue := transport.NewOutboundQueue(nil, nil, logger)
		require.NotNil(t, queue)
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &transport.OutboundQueueConfig{
			MaxAttempts:  3,
			ProcessBatch: 5,
			PollInterval: 10 * time.Second,
			RetryDelays:  []time.Duration{30 * time.Second, 1 * time.Minute},
		}
		queue := transport.NewOutboundQueue(nil, config, logger)
		require.NotNil(t, queue)
	})
}

func TestQueueStatusConstants(t *testing.T) {
	assert.Equal(t, transport.QueueStatus("pending"), transport.QueueStatusPending)
	assert.Equal(t, transport.QueueStatus("processing"), transport.QueueStatusProcessing)
	assert.Equal(t, transport.QueueStatus("sent"), transport.QueueStatusSent)
	assert.Equal(t, transport.QueueStatus("failed"), transport.QueueStatusFailed)
	assert.Equal(t, transport.QueueStatus("retry"), transport.QueueStatusRetry)
}

func TestQueuedMessageStruct(t *testing.T) {
	now := time.Now()
	msg := &transport.QueuedMessage{
		ID:          "test-id-123",
		MessageID:   "<test@example.com>",
		From:        "sender@example.com",
		To:          []string{"recipient1@example.com", "recipient2@example.com"},
		RawMessage:  []byte("From: sender@example.com\r\nTo: recipient1@example.com\r\n\r\nTest body"),
		Status:      transport.QueueStatusPending,
		Attempts:    0,
		MaxAttempts: 5,
		NextAttempt: now,
		CreatedAt:   now,
		Metadata:    map[string]string{"key": "value"},
	}

	assert.Equal(t, "test-id-123", msg.ID)
	assert.Equal(t, "<test@example.com>", msg.MessageID)
	assert.Equal(t, "sender@example.com", msg.From)
	assert.Len(t, msg.To, 2)
	assert.Equal(t, transport.QueueStatusPending, msg.Status)
	assert.Equal(t, 0, msg.Attempts)
	assert.Equal(t, 5, msg.MaxAttempts)
	assert.Equal(t, "value", msg.Metadata["key"])
}

func TestQueueStatsStruct(t *testing.T) {
	stats := &transport.QueueStats{
		Pending:    10,
		Processing: 2,
		Retry:      3,
		Sent:       100,
		Failed:     5,
	}

	assert.Equal(t, int64(10), stats.Pending)
	assert.Equal(t, int64(2), stats.Processing)
	assert.Equal(t, int64(3), stats.Retry)
	assert.Equal(t, int64(100), stats.Sent)
	assert.Equal(t, int64(5), stats.Failed)
}

// Note: Tests that require database operations (Enqueue, Dequeue, MarkSent, MarkFailed, etc.)
// are in the integration tests since they need a real PostgreSQL connection.
// See tests/integration/queue_test.go for those tests.
