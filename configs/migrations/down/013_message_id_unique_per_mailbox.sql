-- Down for 013_message_id_unique_per_mailbox. NOT run by the migrator (it only
-- reads migrations/*.sql); apply by hand, then delete the schema_migrations row:
--
--   DELETE FROM email.schema_migrations WHERE version = '013_message_id_unique_per_mailbox';
--
-- Restoring the table-wide constraint FAILS if any Message-ID now appears in
-- more than one row, which is exactly what 013 allows (a Sent copy plus inbox
-- copies). Find them first and decide which copies to keep:
--
--   SELECT message_id, count(*) FROM email.emails
--   WHERE message_id IS NOT NULL GROUP BY 1 HAVING count(*) > 1;

BEGIN;
ALTER TABLE email.emails ADD CONSTRAINT emails_message_id_key UNIQUE (message_id);
DROP INDEX IF EXISTS email.emails_mailbox_direction_message_id_key;
COMMIT;
