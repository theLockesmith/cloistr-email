-- Migration 007: served domains for multi-domain / bring-your-own-domain.
--
-- Each row is a domain this instance accepts/sends mail for, with its own DKIM
-- keypair so outbound From: <user>@<domain> is signed with d=<domain> (DKIM
-- alignment). Replaces the single DKIM_DOMAIN config for multi-domain hosting.
--
-- SECURITY: dkim_private_key is a PEM RSA private key at rest. Encrypting it (or
-- storing only a secret reference) is a follow-up before self-serve BYO.

CREATE TABLE IF NOT EXISTS domains (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    domain VARCHAR(255) NOT NULL UNIQUE,
    dkim_selector VARCHAR(63) NOT NULL DEFAULT 'mail',
    dkim_private_key TEXT,
    verified BOOLEAN NOT NULL DEFAULT FALSE,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_domains_active ON domains(active);
