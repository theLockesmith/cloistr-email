-- Migration 006: widen npub columns to hold 64-char hex pubkeys.
--
-- The schema declared these as VARCHAR(63) (sized for bech32 "npub1..."), but
-- the service stores 64-char hex public keys throughout (req.SenderNpub, the
-- NIP-46 session pubkey, etc.). Inserting a 64-char hex value into VARCHAR(63)
-- fails with "value too long", which silently broke persistence of sent emails
-- (CreateEmail's sender_npub) and user creation. Widen to 128 for headroom.

ALTER TABLE users           ALTER COLUMN npub           TYPE VARCHAR(128);
ALTER TABLE emails          ALTER COLUMN sender_npub    TYPE VARCHAR(128);
ALTER TABLE emails          ALTER COLUMN recipient_npub TYPE VARCHAR(128);
ALTER TABLE encryption_keys ALTER COLUMN contact_npub   TYPE VARCHAR(128);
ALTER TABLE nip05_cache     ALTER COLUMN npub           TYPE VARCHAR(128);
ALTER TABLE contacts        ALTER COLUMN npub           TYPE VARCHAR(128);
