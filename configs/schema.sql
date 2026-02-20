-- coldforge-email Database Schema

-- Users table
-- Stores Nostr identities and email accounts
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    npub VARCHAR(63) NOT NULL UNIQUE, -- Nostr public key (bech32 encoded)
    email VARCHAR(255) NOT NULL UNIQUE,
    email_verified BOOLEAN DEFAULT FALSE,
    email_verified_at TIMESTAMP,
    public_key TEXT NOT NULL,
    encryption_method VARCHAR(50) DEFAULT 'nip44',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP
);

CREATE INDEX idx_users_npub ON users(npub);
CREATE INDEX idx_users_email ON users(email);

-- Sessions table
-- Stores authenticated sessions
CREATE TABLE IF NOT EXISTS sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token VARCHAR(255) NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    deleted_at TIMESTAMP
);

CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_token ON sessions(token);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);

-- Emails table
-- Stores email metadata and encrypted bodies
CREATE TABLE IF NOT EXISTS emails (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message_id VARCHAR(255) UNIQUE,
    from_address VARCHAR(255) NOT NULL,
    to_address VARCHAR(255) NOT NULL,
    cc VARCHAR(255),
    bcc VARCHAR(255),
    subject TEXT,
    body TEXT,
    html_body TEXT,
    is_encrypted BOOLEAN DEFAULT FALSE,
    encryption_nonce VARCHAR(255),
    sender_npub VARCHAR(63),
    recipient_npub VARCHAR(63),
    -- Email direction and status
    direction VARCHAR(20) DEFAULT 'sent', -- sent, received, draft
    status VARCHAR(20) DEFAULT 'active', -- active, deleted, archived, spam
    read_at TIMESTAMP,
    -- Email organization
    folder VARCHAR(50) DEFAULT 'INBOX',
    labels TEXT[], -- Array of custom labels
    -- Nostr signature verification (RFC-002)
    nostr_verified BOOLEAN DEFAULT FALSE,
    nostr_verification_error TEXT,
    nostr_verified_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP
);

CREATE INDEX idx_emails_user_id ON emails(user_id);
CREATE INDEX idx_emails_from ON emails(from_address);
CREATE INDEX idx_emails_to ON emails(to_address);
CREATE INDEX idx_emails_status ON emails(status);
CREATE INDEX idx_emails_created_at ON emails(created_at DESC);
CREATE INDEX idx_emails_sender_npub ON emails(sender_npub);
CREATE INDEX idx_emails_recipient_npub ON emails(recipient_npub);

-- Attachments table
-- References to Blossom file storage
CREATE TABLE IF NOT EXISTS attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email_id UUID NOT NULL REFERENCES emails(id) ON DELETE CASCADE,
    filename VARCHAR(255) NOT NULL,
    content_type VARCHAR(100),
    size_bytes BIGINT,
    -- Blossom reference
    blossom_sha256 VARCHAR(64),
    blossom_url TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_attachments_email_id ON attachments(email_id);

-- Contacts table
-- Address book for users
CREATE TABLE IF NOT EXISTS contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    name VARCHAR(255),
    npub VARCHAR(63),
    notes TEXT,
    -- Organization info
    organization VARCHAR(255),
    phone VARCHAR(20),
    -- Contact preferences
    always_encrypt BOOLEAN DEFAULT FALSE,
    blocked BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP,
    UNIQUE(user_id, email)
);

CREATE INDEX idx_contacts_user_id ON contacts(user_id);
CREATE INDEX idx_contacts_email ON contacts(email);
CREATE INDEX idx_contacts_npub ON contacts(npub);
CREATE INDEX idx_contacts_name ON contacts(name);

-- NIP-05 Cache
-- Caches NIP-05 key discovery lookups
CREATE TABLE IF NOT EXISTS nip05_cache (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL UNIQUE,
    npub VARCHAR(63),
    -- Cache validity
    cached_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP,
    valid BOOLEAN DEFAULT TRUE
);

CREATE INDEX idx_nip05_cache_email ON nip05_cache(email);
CREATE INDEX idx_nip05_cache_expires_at ON nip05_cache(expires_at);

-- Encryption Keys table
-- Stores imported and generated encryption keys
CREATE TABLE IF NOT EXISTS encryption_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    contact_npub VARCHAR(63),
    public_key TEXT NOT NULL,
    -- Key metadata
    key_type VARCHAR(50) DEFAULT 'nip44',
    imported BOOLEAN DEFAULT TRUE,
    verified BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_encryption_keys_user_id ON encryption_keys(user_id);
CREATE INDEX idx_encryption_keys_contact_npub ON encryption_keys(contact_npub);

-- Email Templates table
-- For signature templates and canned responses
CREATE TABLE IF NOT EXISTS email_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    subject VARCHAR(255),
    body TEXT NOT NULL,
    is_signature BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP
);

CREATE INDEX idx_email_templates_user_id ON email_templates(user_id);

-- Audit Log
-- Tracks important actions for security
CREATE TABLE IF NOT EXISTS audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(50) NOT NULL,
    resource_type VARCHAR(50),
    resource_id VARCHAR(255),
    details JSONB,
    ip_address VARCHAR(45),
    user_agent TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_audit_log_user_id ON audit_log(user_id);
CREATE INDEX idx_audit_log_action ON audit_log(action);
CREATE INDEX idx_audit_log_created_at ON audit_log(created_at DESC);

-- Update trigger for updated_at timestamps
CREATE OR REPLACE FUNCTION update_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_users_timestamp BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_timestamp();

CREATE TRIGGER update_emails_timestamp BEFORE UPDATE ON emails
    FOR EACH ROW EXECUTE FUNCTION update_timestamp();

CREATE TRIGGER update_contacts_timestamp BEFORE UPDATE ON contacts
    FOR EACH ROW EXECUTE FUNCTION update_timestamp();

CREATE TRIGGER update_encryption_keys_timestamp BEFORE UPDATE ON encryption_keys
    FOR EACH ROW EXECUTE FUNCTION update_timestamp();

CREATE TRIGGER update_email_templates_timestamp BEFORE UPDATE ON email_templates
    FOR EACH ROW EXECUTE FUNCTION update_timestamp();

-- Outbound Queue table
-- Persistent queue for outbound email with retry support (RFC-001 Phase 2)
CREATE TABLE IF NOT EXISTS outbound_queue (
    id VARCHAR(64) PRIMARY KEY,
    message_id VARCHAR(255) NOT NULL,
    sender VARCHAR(255) NOT NULL,
    recipients JSONB NOT NULL,
    raw_message BYTEA NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    last_attempt TIMESTAMP,
    next_attempt TIMESTAMP NOT NULL DEFAULT NOW(),
    last_error TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    metadata JSONB DEFAULT '{}'::jsonb
);

CREATE INDEX idx_outbound_queue_status ON outbound_queue(status);
CREATE INDEX idx_outbound_queue_next_attempt ON outbound_queue(next_attempt) WHERE status IN ('pending', 'retry');
CREATE INDEX idx_outbound_queue_message_id ON outbound_queue(message_id);
CREATE INDEX idx_outbound_queue_created_at ON outbound_queue(created_at);

-- Email bounces table
-- Tracks bounce information for outbound email delivery failures
CREATE TABLE IF NOT EXISTS email_bounces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    original_recipient VARCHAR(255) NOT NULL,
    original_message_id VARCHAR(255),
    bounce_type VARCHAR(20) NOT NULL DEFAULT 'unknown',
    reason TEXT,
    diagnostic_code VARCHAR(50),
    remote_server VARCHAR(255),
    received_at TIMESTAMP NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_email_bounces_recipient ON email_bounces(original_recipient);
CREATE INDEX idx_email_bounces_message_id ON email_bounces(original_message_id);
CREATE INDEX idx_email_bounces_type ON email_bounces(bounce_type);
CREATE INDEX idx_email_bounces_received_at ON email_bounces(received_at DESC);
