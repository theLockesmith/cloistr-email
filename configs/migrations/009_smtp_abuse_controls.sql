-- 009: SMTP abuse-control columns (audit C enforcement).
--
-- Adds the per-account send-state to mailboxes and sender attribution to
-- bounces, so the rate limiter / detection ladder can key everything on the
-- sender's pubkey (never IP — privacy constraint).
--
-- Tables live in the `email` schema (search_path = email,public).

BEGIN;

-- Sender attribution on bounces: needed to compute per-account bounce rate.
-- Nullable — historical bounces and third-party inbound bounces have no sender
-- pubkey. Indexed for the bounce-rate aggregation per account.
ALTER TABLE email_bounces ADD COLUMN IF NOT EXISTS sender_pubkey CHAR(64);
CREATE INDEX IF NOT EXISTS idx_email_bounces_sender_pubkey
    ON email_bounces(sender_pubkey) WHERE sender_pubkey IS NOT NULL;

-- Per-account send state on the (email-owned) mailboxes table.
--   send_enabled       — false = held/suspended at the send gate (email-local;
--                        distinct from users.enabled which is the platform-wide
--                        suspend hammer owned by cloistr-me).
--   send_elevated      — true = elevated limits (paid / WoT-vouched / operator).
--   send_suspended_at  — when the suspend ladder last suspended this account
--                        (null = not suspended); drives warn/throttle/hold state.
ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS send_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS send_elevated BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE mailboxes ADD COLUMN IF NOT EXISTS send_suspended_at TIMESTAMP;

-- Fast lookup of currently-suspended accounts for the reconciler / admin views.
CREATE INDEX IF NOT EXISTS idx_mailboxes_send_suspended_at
    ON mailboxes(send_suspended_at) WHERE send_suspended_at IS NOT NULL;

COMMIT;
