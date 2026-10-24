-- emails.message_id was UNIQUE across the WHOLE table (emails_message_id_key),
-- but one send legitimately stores several rows with the same Message-ID: the
-- sender's Sent copy plus one inbox copy per local recipient. Every copy after
-- the first failed the constraint and was dropped (a Cc to yourself, a second
-- local recipient, or the Sent copy itself, whichever landed second).
--
-- A Message-ID is unique per mailbox AND direction: sending to yourself puts a
-- 'sent' and a 'received' row with the same ID in the same mailbox, both
-- legitimate. A second delivery of the same message into the same mailbox and
-- direction is still refused, as before.
--
-- Built in the migrator's single transaction (not CONCURRENTLY: the migrator
-- holds a transaction-scoped advisory lock because production connects through
-- pgbouncer in transaction mode, and CREATE INDEX CONCURRENTLY cannot run in a
-- transaction). Building the new index and dropping the old constraint in one
-- transaction also means there is no moment without a uniqueness guard, and a
-- failure rolls both back. The table is small; the build holds a SHARE lock on
-- email.emails for its duration, bounded by the migrator's lock_timeout (10s)
-- and statement_timeout (5min).
--
-- Down: configs/migrations/down/013_message_id_unique_per_mailbox.sql (not run
-- by the migrator; apply by hand).

CREATE UNIQUE INDEX IF NOT EXISTS emails_mailbox_direction_message_id_key
    ON email.emails (mailbox_pubkey, direction, message_id);

ALTER TABLE email.emails DROP CONSTRAINT IF EXISTS emails_message_id_key;
