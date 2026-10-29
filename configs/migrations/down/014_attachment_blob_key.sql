-- Down for 014_attachment_blob_key. NOT run by the migrator (it only reads
-- migrations/*.sql); apply by hand, then delete the schema_migrations row:
--
--   DELETE FROM email.schema_migrations WHERE version = '014_attachment_blob_key';
--
-- Dropping the column DESTROYS the only copy of every sealed blob's key: those
-- attachments can never be opened again. Check first:
--
--   SELECT count(*) FROM email.attachments WHERE blob_key IS NOT NULL;

ALTER TABLE email.attachments DROP COLUMN IF EXISTS blob_key;
