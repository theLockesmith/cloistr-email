-- Encrypted attachment blobs.
--
-- Blossom serves any blob to anyone who knows its hash, so attachments the
-- server stores there are sealed first (internal/blobseal): each blob gets its
-- own random key, and that key is kept here wrapped under the server's
-- attachment KEK. Rows without a key were never sealed by the server (client
-- end-to-end attachments, server-mode NIP-44 copies, or metadata-only rows).
--
-- Additive and idempotent: one nullable column, no backfill. No production row
-- references a Blossom blob yet (BLOSSOM_SERVERS has never been set).

ALTER TABLE email.attachments
    ADD COLUMN IF NOT EXISTS blob_key TEXT;

COMMENT ON COLUMN email.attachments.blob_key IS
    'Per-blob random key wrapped under ATTACHMENT_KEK (blobseal v1). NULL = blob not sealed by the server.';
