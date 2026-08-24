-- Threading and starred support.
--
-- in_reply_to / references: RFC 2822 threading headers, now persisted so the
-- web client can group messages into conversations.
--
-- The inbound parser already extracts these from the MIME headers; the outbound
-- path already passes them in the transport message.  Both stored them nowhere.
-- This migration adds the columns; the application code is updated in the same
-- commit to populate them on both paths.
--
-- For starred: we model it as a label ("\\Starred", Gmail convention) using the
-- existing labels TEXT[] column.  No new column needed; the application adds or
-- removes the label element via array operators.

ALTER TABLE email.emails
    ADD COLUMN IF NOT EXISTS in_reply_to TEXT,
    ADD COLUMN IF NOT EXISTS references_header TEXT; -- space-separated list of message-IDs

COMMENT ON COLUMN email.emails.in_reply_to IS
    'RFC 2822 In-Reply-To header value; used for conversation threading.';

COMMENT ON COLUMN email.emails.references_header IS
    'RFC 2822 References header value (space-separated); full thread ancestry.';

-- Index on in_reply_to so a thread-lookup (given the parent message-id, find
-- all replies) is a fast index scan rather than a seq-scan.
CREATE INDEX IF NOT EXISTS idx_emails_in_reply_to
    ON email.emails(in_reply_to) WHERE in_reply_to IS NOT NULL;
