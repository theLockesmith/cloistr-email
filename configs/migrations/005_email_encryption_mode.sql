-- Migration 005: record how each email's body is encrypted at rest.
--
-- Previously GetEmail inferred the encryption mode from whether sender_npub
-- was set, which was unreliable and (combined with server-side sends storing
-- plaintext) caused encrypted reads to fail and leak plaintext. We now persist
-- the mode explicitly and encrypt the stored server-side body at rest.
--
-- Existing rows get NULL, which the read path treats as legacy/none.

ALTER TABLE emails
  ADD COLUMN IF NOT EXISTS encryption_mode VARCHAR(16);
