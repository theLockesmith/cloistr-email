-- 008: Retire cloistr-email's private identity table in favor of the shared
-- platform identity owned by cloistr-me.
--
-- Model (see docs handoff "identity alignment + aliases"):
--   * Shared identity = public.users (pubkey PK) + public.addresses.
--   * Mailbox = per-pubkey. ONE mailbox per user.
--   * Addresses (primary + aliases) are delivery ROUTES into that one mailbox.
--     Recipient resolution is: username@domain -> public.addresses -> pubkey
--     -> email.mailboxes. Every active alias delivers to the same mailbox.
--
-- Why no FK to public.users(pubkey):
--   the cloistr_email role holds SELECT on public.users/public.addresses but
--   NOT REFERENCES, so a cross-schema FK cannot be created. Ownership
--   integrity is enforced at the application layer via address resolution.
--
-- Safety: every email.* table is empty at migration time (verified 0 rows:
--   users/emails/sessions/contacts/encryption_keys/email_templates/audit_log),
--   so columns are dropped and re-added rather than backfilled.

BEGIN;

-- The one mailbox per pubkey.
CREATE TABLE IF NOT EXISTS mailboxes (
    pubkey       CHAR(64) PRIMARY KEY,
    display_name VARCHAR(255),
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at   TIMESTAMP,
    CONSTRAINT mailboxes_pubkey_hex CHECK (pubkey ~ '^[0-9a-f]{64}$')
);

-- Re-key every table that referenced the private users table.
-- Dropping user_id also drops its dependent indexes and FK constraints.

-- emails: the core mailbox contents.
ALTER TABLE emails DROP COLUMN IF EXISTS user_id;
ALTER TABLE emails ADD COLUMN mailbox_pubkey CHAR(64) NOT NULL
    REFERENCES mailboxes(pubkey) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_emails_mailbox_pubkey ON emails(mailbox_pubkey);

-- contacts: per-mailbox address book. Dropping user_id also drops the
-- UNIQUE(user_id, email) constraint, so the mailbox-scoped equivalent is
-- restored explicitly.
ALTER TABLE contacts DROP COLUMN IF EXISTS user_id;
ALTER TABLE contacts ADD COLUMN mailbox_pubkey CHAR(64) NOT NULL
    REFERENCES mailboxes(pubkey) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_contacts_mailbox_pubkey ON contacts(mailbox_pubkey);
ALTER TABLE contacts ADD CONSTRAINT contacts_mailbox_email_key
    UNIQUE (mailbox_pubkey, email);

-- encryption_keys: per-mailbox cached contact keys.
ALTER TABLE encryption_keys DROP COLUMN IF EXISTS user_id;
ALTER TABLE encryption_keys ADD COLUMN mailbox_pubkey CHAR(64) NOT NULL
    REFERENCES mailboxes(pubkey) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_encryption_keys_mailbox_pubkey ON encryption_keys(mailbox_pubkey);

-- email_templates: per-mailbox templates.
ALTER TABLE email_templates DROP COLUMN IF EXISTS user_id;
ALTER TABLE email_templates ADD COLUMN mailbox_pubkey CHAR(64) NOT NULL
    REFERENCES mailboxes(pubkey) ON DELETE CASCADE;

-- sessions: retained for parity (live sessions are in Redis).
ALTER TABLE sessions DROP COLUMN IF EXISTS user_id;
ALTER TABLE sessions ADD COLUMN mailbox_pubkey CHAR(64) NOT NULL
    REFERENCES mailboxes(pubkey) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_sessions_mailbox_pubkey ON sessions(mailbox_pubkey);

-- audit_log: actor is nullable (system events have no mailbox).
ALTER TABLE audit_log DROP COLUMN IF EXISTS user_id;
ALTER TABLE audit_log ADD COLUMN mailbox_pubkey CHAR(64)
    REFERENCES mailboxes(pubkey) ON DELETE SET NULL;

-- The private identity table is now unreferenced. Shared identity wins.
DROP TABLE IF EXISTS users;

COMMIT;
