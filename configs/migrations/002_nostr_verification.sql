-- Migration: Add Nostr signature verification columns
-- RFC-002: Nostr as the Identity Layer for SMTP

-- Add verification columns to emails table
ALTER TABLE emails ADD COLUMN IF NOT EXISTS nostr_verified BOOLEAN DEFAULT FALSE;
ALTER TABLE emails ADD COLUMN IF NOT EXISTS nostr_verification_error TEXT;
ALTER TABLE emails ADD COLUMN IF NOT EXISTS nostr_verified_at TIMESTAMP;

-- Add index for verified emails
CREATE INDEX IF NOT EXISTS idx_emails_nostr_verified ON emails(nostr_verified);
