-- 010: Feedback-loop (FBL) complaint ingestion.
--
-- When a recipient at a large provider hits "report spam", enrolled senders
-- receive an Abuse Reporting Format report (RFC 5965). Complaint rate is a
-- stronger abuse signal than bounce rate — a bounce means the address was
-- wrong, a complaint means a real person did not want the mail — and providers
-- begin throttling around 0.1%, far below anything a bounce rate would flag.
--
-- Rows only appear once the sending domains are enrolled in each provider's FBL
-- programme; the table is harmless while empty.
--
-- Tables live in the `email` schema (search_path = email,public).

BEGIN;

CREATE TABLE IF NOT EXISTS email_complaints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    original_recipient VARCHAR(255),
    original_message_id VARCHAR(255),
    -- ARF Feedback-Type: abuse, fraud, virus, other
    feedback_type VARCHAR(32) NOT NULL DEFAULT 'other',
    reporting_mta VARCHAR(255),
    received_at TIMESTAMP NOT NULL DEFAULT NOW(),
    -- Sender attribution, same as email_bounces. Nullable: providers routinely
    -- redact the recipient, and a report we cannot tie to an account must be
    -- kept for operator review rather than charged to the wrong sender.
    sender_pubkey CHAR(64)
);

CREATE INDEX IF NOT EXISTS idx_email_complaints_sender_pubkey
    ON email_complaints(sender_pubkey) WHERE sender_pubkey IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_email_complaints_received_at
    ON email_complaints(received_at DESC);

COMMENT ON TABLE email_complaints IS
    'Feedback-loop (ARF) spam complaints, attributed to the sending account';

COMMIT;
