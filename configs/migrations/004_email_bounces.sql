-- Email bounces table
-- Tracks bounce information for outbound email delivery failures

CREATE TABLE IF NOT EXISTS email_bounces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    original_recipient VARCHAR(255) NOT NULL,
    original_message_id VARCHAR(255),
    bounce_type VARCHAR(20) NOT NULL DEFAULT 'unknown',
    reason TEXT,
    diagnostic_code VARCHAR(50),
    remote_server VARCHAR(255),
    received_at TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_email_bounces_recipient ON email_bounces(original_recipient);
CREATE INDEX idx_email_bounces_message_id ON email_bounces(original_message_id);
CREATE INDEX idx_email_bounces_type ON email_bounces(bounce_type);
CREATE INDEX idx_email_bounces_received_at ON email_bounces(received_at DESC);

-- Add comment
COMMENT ON TABLE email_bounces IS 'Tracks bounce information for outbound email delivery failures';
