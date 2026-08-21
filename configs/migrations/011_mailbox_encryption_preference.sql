-- Per-user preferred encryption mode.
--
-- The Settings screen has always offered an encryption-mode control, and it was
-- never wired to anything: the UI posted to /api/v1/encryption/preferred-mode,
-- no such route existed, and the request 404'd silently. On a product whose
-- entire proposition is that your mail is encrypted, a preference control that
-- pretends to save is worse than no control at all.
--
-- encryption_mode already exists PER EMAIL. This is the per-mailbox DEFAULT
-- applied to new sends, which is what the setting was always describing.
--
-- Nullable with no default on purpose: NULL means "no explicit preference,
-- use the service default", which is distinct from a user deliberately
-- choosing the mode that happens to match today's default. If the service
-- default changes later, that distinction is the difference between honouring
-- a choice and silently overriding one.
ALTER TABLE email.mailboxes
    ADD COLUMN IF NOT EXISTS preferred_encryption_mode TEXT;

COMMENT ON COLUMN email.mailboxes.preferred_encryption_mode IS
    'User default encryption mode for new sends (e2e|server|none). NULL = use service default.';
