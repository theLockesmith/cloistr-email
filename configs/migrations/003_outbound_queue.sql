-- Migration: Add outbound email queue table
-- RFC-001 Phase 2: Own outbound delivery

-- Create outbound queue table
CREATE TABLE IF NOT EXISTS outbound_queue (
    id VARCHAR(64) PRIMARY KEY,
    message_id VARCHAR(255) NOT NULL,
    sender VARCHAR(255) NOT NULL,
    recipients JSONB NOT NULL,
    raw_message BYTEA NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    last_attempt TIMESTAMP,
    next_attempt TIMESTAMP NOT NULL DEFAULT NOW(),
    last_error TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    metadata JSONB DEFAULT '{}'::jsonb
);

-- Indexes for efficient queue operations
CREATE INDEX IF NOT EXISTS idx_outbound_queue_status ON outbound_queue(status);
CREATE INDEX IF NOT EXISTS idx_outbound_queue_next_attempt ON outbound_queue(next_attempt) WHERE status IN ('pending', 'retry');
CREATE INDEX IF NOT EXISTS idx_outbound_queue_message_id ON outbound_queue(message_id);
CREATE INDEX IF NOT EXISTS idx_outbound_queue_created_at ON outbound_queue(created_at);

-- Add comment
COMMENT ON TABLE outbound_queue IS 'Persistent queue for outbound email with retry support';
