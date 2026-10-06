-- Production email schema (schema only, no data), pg_dump of cloistr db schema
-- `email` taken 2026-10-06 after migrations 010+011 were applied by hand.
-- Test fixture for the migrator: running it against this must change nothing.
-- search_path for cloistr_email: email, public
--
-- PostgreSQL database dump
--


-- Dumped from database version 17.7 (Ubuntu 17.7-3.pgdg24.04+1)
-- Dumped by pg_dump version 17.11

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: email; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA email;


--
-- Name: update_timestamp(); Type: FUNCTION; Schema: email; Owner: -
--

CREATE FUNCTION email.update_timestamp() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: attachments; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.attachments (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email_id uuid NOT NULL,
    filename character varying(255) NOT NULL,
    content_type character varying(100),
    size_bytes bigint,
    blossom_sha256 character varying(64),
    blossom_url text,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


--
-- Name: audit_log; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.audit_log (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    action character varying(50) NOT NULL,
    resource_type character varying(50),
    resource_id character varying(255),
    details jsonb,
    ip_address character varying(45),
    user_agent text,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    mailbox_pubkey character(64)
);


--
-- Name: contacts; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.contacts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email character varying(255) NOT NULL,
    name character varying(255),
    npub character varying(128),
    notes text,
    organization character varying(255),
    phone character varying(20),
    always_encrypt boolean DEFAULT false,
    blocked boolean DEFAULT false,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    deleted_at timestamp without time zone,
    mailbox_pubkey character(64) NOT NULL
);


--
-- Name: domains; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.domains (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    domain character varying(255) NOT NULL,
    dkim_selector character varying(63) DEFAULT 'mail'::character varying NOT NULL,
    dkim_private_key text,
    verified boolean DEFAULT false NOT NULL,
    active boolean DEFAULT true NOT NULL,
    created_at timestamp without time zone DEFAULT now() NOT NULL,
    updated_at timestamp without time zone DEFAULT now() NOT NULL
);


--
-- Name: email_bounces; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.email_bounces (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    original_recipient character varying(255) NOT NULL,
    original_message_id character varying(255),
    bounce_type character varying(20) DEFAULT 'unknown'::character varying NOT NULL,
    reason text,
    diagnostic_code character varying(50),
    remote_server character varying(255),
    received_at timestamp without time zone DEFAULT now() NOT NULL,
    created_at timestamp without time zone DEFAULT now() NOT NULL,
    sender_pubkey character(64)
);


--
-- Name: email_complaints; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.email_complaints (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    original_recipient character varying(255),
    original_message_id character varying(255),
    feedback_type character varying(32) DEFAULT 'other'::character varying NOT NULL,
    reporting_mta character varying(255),
    received_at timestamp without time zone DEFAULT now() NOT NULL,
    sender_pubkey character(64)
);


--
-- Name: TABLE email_complaints; Type: COMMENT; Schema: email; Owner: -
--

COMMENT ON TABLE email.email_complaints IS 'Feedback-loop (ARF) spam complaints, attributed to the sending account';


--
-- Name: email_templates; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.email_templates (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name character varying(255) NOT NULL,
    subject character varying(255),
    body text NOT NULL,
    is_signature boolean DEFAULT false,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    deleted_at timestamp without time zone,
    mailbox_pubkey character(64) NOT NULL
);


--
-- Name: emails; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.emails (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    message_id character varying(255),
    from_address character varying(255) NOT NULL,
    to_address character varying(255) NOT NULL,
    cc character varying(255),
    bcc character varying(255),
    subject text,
    body text,
    html_body text,
    is_encrypted boolean DEFAULT false,
    encryption_nonce character varying(255),
    encryption_mode character varying(16),
    sender_npub character varying(128),
    recipient_npub character varying(128),
    direction character varying(20) DEFAULT 'sent'::character varying,
    status character varying(20) DEFAULT 'active'::character varying,
    read_at timestamp without time zone,
    folder character varying(50) DEFAULT 'INBOX'::character varying,
    labels text[],
    nostr_verified boolean DEFAULT false,
    nostr_verification_error text,
    nostr_verified_at timestamp without time zone,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    deleted_at timestamp without time zone,
    mailbox_pubkey character(64) NOT NULL,
    in_reply_to text,
    references_header text
);


--
-- Name: COLUMN emails.in_reply_to; Type: COMMENT; Schema: email; Owner: -
--

COMMENT ON COLUMN email.emails.in_reply_to IS 'RFC 2822 In-Reply-To header value; used for conversation threading.';


--
-- Name: COLUMN emails.references_header; Type: COMMENT; Schema: email; Owner: -
--

COMMENT ON COLUMN email.emails.references_header IS 'RFC 2822 References header value (space-separated); full thread ancestry.';


--
-- Name: encryption_keys; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.encryption_keys (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    contact_npub character varying(128),
    public_key text NOT NULL,
    key_type character varying(50) DEFAULT 'nip44'::character varying,
    imported boolean DEFAULT true,
    verified boolean DEFAULT false,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    mailbox_pubkey character(64) NOT NULL
);


--
-- Name: mailboxes; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.mailboxes (
    pubkey character(64) NOT NULL,
    display_name character varying(255),
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    deleted_at timestamp without time zone,
    send_enabled boolean DEFAULT true NOT NULL,
    send_elevated boolean DEFAULT false NOT NULL,
    send_suspended_at timestamp without time zone,
    preferred_encryption_mode text,
    CONSTRAINT mailboxes_pubkey_hex CHECK ((pubkey ~ '^[0-9a-f]{64}$'::text))
);


--
-- Name: COLUMN mailboxes.preferred_encryption_mode; Type: COMMENT; Schema: email; Owner: -
--

COMMENT ON COLUMN email.mailboxes.preferred_encryption_mode IS 'User default encryption mode for new sends (e2e|server|none). NULL = use service default.';


--
-- Name: nip05_cache; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.nip05_cache (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email character varying(255) NOT NULL,
    npub character varying(128),
    cached_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamp without time zone,
    valid boolean DEFAULT true
);


--
-- Name: outbound_queue; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.outbound_queue (
    id character varying(64) NOT NULL,
    message_id character varying(255) NOT NULL,
    sender character varying(255) NOT NULL,
    recipients jsonb NOT NULL,
    raw_message bytea NOT NULL,
    status character varying(20) DEFAULT 'pending'::character varying NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 5 NOT NULL,
    last_attempt timestamp without time zone,
    next_attempt timestamp without time zone DEFAULT now() NOT NULL,
    last_error text,
    created_at timestamp without time zone DEFAULT now() NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb
);


--
-- Name: sessions; Type: TABLE; Schema: email; Owner: -
--

CREATE TABLE email.sessions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    token character varying(255) NOT NULL,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamp without time zone NOT NULL,
    deleted_at timestamp without time zone,
    mailbox_pubkey character(64) NOT NULL
);


--
-- Name: attachments attachments_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.attachments
    ADD CONSTRAINT attachments_pkey PRIMARY KEY (id);


--
-- Name: audit_log audit_log_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.audit_log
    ADD CONSTRAINT audit_log_pkey PRIMARY KEY (id);


--
-- Name: contacts contacts_mailbox_email_key; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.contacts
    ADD CONSTRAINT contacts_mailbox_email_key UNIQUE (mailbox_pubkey, email);


--
-- Name: contacts contacts_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.contacts
    ADD CONSTRAINT contacts_pkey PRIMARY KEY (id);


--
-- Name: domains domains_domain_key; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.domains
    ADD CONSTRAINT domains_domain_key UNIQUE (domain);


--
-- Name: domains domains_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.domains
    ADD CONSTRAINT domains_pkey PRIMARY KEY (id);


--
-- Name: email_bounces email_bounces_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.email_bounces
    ADD CONSTRAINT email_bounces_pkey PRIMARY KEY (id);


--
-- Name: email_complaints email_complaints_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.email_complaints
    ADD CONSTRAINT email_complaints_pkey PRIMARY KEY (id);


--
-- Name: email_templates email_templates_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.email_templates
    ADD CONSTRAINT email_templates_pkey PRIMARY KEY (id);


--
-- Name: emails emails_message_id_key; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.emails
    ADD CONSTRAINT emails_message_id_key UNIQUE (message_id);


--
-- Name: emails emails_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.emails
    ADD CONSTRAINT emails_pkey PRIMARY KEY (id);


--
-- Name: encryption_keys encryption_keys_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.encryption_keys
    ADD CONSTRAINT encryption_keys_pkey PRIMARY KEY (id);


--
-- Name: mailboxes mailboxes_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.mailboxes
    ADD CONSTRAINT mailboxes_pkey PRIMARY KEY (pubkey);


--
-- Name: nip05_cache nip05_cache_email_key; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.nip05_cache
    ADD CONSTRAINT nip05_cache_email_key UNIQUE (email);


--
-- Name: nip05_cache nip05_cache_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.nip05_cache
    ADD CONSTRAINT nip05_cache_pkey PRIMARY KEY (id);


--
-- Name: outbound_queue outbound_queue_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.outbound_queue
    ADD CONSTRAINT outbound_queue_pkey PRIMARY KEY (id);


--
-- Name: sessions sessions_pkey; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.sessions
    ADD CONSTRAINT sessions_pkey PRIMARY KEY (id);


--
-- Name: sessions sessions_token_key; Type: CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.sessions
    ADD CONSTRAINT sessions_token_key UNIQUE (token);


--
-- Name: idx_attachments_email_id; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_attachments_email_id ON email.attachments USING btree (email_id);


--
-- Name: idx_audit_log_action; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_audit_log_action ON email.audit_log USING btree (action);


--
-- Name: idx_audit_log_created_at; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_audit_log_created_at ON email.audit_log USING btree (created_at DESC);


--
-- Name: idx_contacts_email; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_contacts_email ON email.contacts USING btree (email);


--
-- Name: idx_contacts_mailbox_pubkey; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_contacts_mailbox_pubkey ON email.contacts USING btree (mailbox_pubkey);


--
-- Name: idx_contacts_name; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_contacts_name ON email.contacts USING btree (name);


--
-- Name: idx_contacts_npub; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_contacts_npub ON email.contacts USING btree (npub);


--
-- Name: idx_domains_active; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_domains_active ON email.domains USING btree (active);


--
-- Name: idx_email_bounces_message_id; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_email_bounces_message_id ON email.email_bounces USING btree (original_message_id);


--
-- Name: idx_email_bounces_received_at; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_email_bounces_received_at ON email.email_bounces USING btree (received_at DESC);


--
-- Name: idx_email_bounces_recipient; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_email_bounces_recipient ON email.email_bounces USING btree (original_recipient);


--
-- Name: idx_email_bounces_sender_pubkey; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_email_bounces_sender_pubkey ON email.email_bounces USING btree (sender_pubkey) WHERE (sender_pubkey IS NOT NULL);


--
-- Name: idx_email_bounces_type; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_email_bounces_type ON email.email_bounces USING btree (bounce_type);


--
-- Name: idx_email_complaints_received_at; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_email_complaints_received_at ON email.email_complaints USING btree (received_at DESC);


--
-- Name: idx_email_complaints_sender_pubkey; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_email_complaints_sender_pubkey ON email.email_complaints USING btree (sender_pubkey) WHERE (sender_pubkey IS NOT NULL);


--
-- Name: idx_emails_created_at; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_emails_created_at ON email.emails USING btree (created_at DESC);


--
-- Name: idx_emails_from; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_emails_from ON email.emails USING btree (from_address);


--
-- Name: idx_emails_in_reply_to; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_emails_in_reply_to ON email.emails USING btree (in_reply_to) WHERE (in_reply_to IS NOT NULL);


--
-- Name: idx_emails_mailbox_pubkey; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_emails_mailbox_pubkey ON email.emails USING btree (mailbox_pubkey);


--
-- Name: idx_emails_recipient_npub; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_emails_recipient_npub ON email.emails USING btree (recipient_npub);


--
-- Name: idx_emails_sender_npub; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_emails_sender_npub ON email.emails USING btree (sender_npub);


--
-- Name: idx_emails_status; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_emails_status ON email.emails USING btree (status);


--
-- Name: idx_emails_to; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_emails_to ON email.emails USING btree (to_address);


--
-- Name: idx_encryption_keys_contact_npub; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_encryption_keys_contact_npub ON email.encryption_keys USING btree (contact_npub);


--
-- Name: idx_encryption_keys_mailbox_pubkey; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_encryption_keys_mailbox_pubkey ON email.encryption_keys USING btree (mailbox_pubkey);


--
-- Name: idx_mailboxes_send_suspended_at; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_mailboxes_send_suspended_at ON email.mailboxes USING btree (send_suspended_at) WHERE (send_suspended_at IS NOT NULL);


--
-- Name: idx_nip05_cache_email; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_nip05_cache_email ON email.nip05_cache USING btree (email);


--
-- Name: idx_nip05_cache_expires_at; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_nip05_cache_expires_at ON email.nip05_cache USING btree (expires_at);


--
-- Name: idx_outbound_queue_created_at; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_outbound_queue_created_at ON email.outbound_queue USING btree (created_at);


--
-- Name: idx_outbound_queue_message_id; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_outbound_queue_message_id ON email.outbound_queue USING btree (message_id);


--
-- Name: idx_outbound_queue_next_attempt; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_outbound_queue_next_attempt ON email.outbound_queue USING btree (next_attempt) WHERE ((status)::text = ANY (ARRAY[('pending'::character varying)::text, ('retry'::character varying)::text]));


--
-- Name: idx_outbound_queue_status; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_outbound_queue_status ON email.outbound_queue USING btree (status);


--
-- Name: idx_sessions_expires_at; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_sessions_expires_at ON email.sessions USING btree (expires_at);


--
-- Name: idx_sessions_mailbox_pubkey; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_sessions_mailbox_pubkey ON email.sessions USING btree (mailbox_pubkey);


--
-- Name: idx_sessions_token; Type: INDEX; Schema: email; Owner: -
--

CREATE INDEX idx_sessions_token ON email.sessions USING btree (token);


--
-- Name: contacts update_contacts_timestamp; Type: TRIGGER; Schema: email; Owner: -
--

CREATE TRIGGER update_contacts_timestamp BEFORE UPDATE ON email.contacts FOR EACH ROW EXECUTE FUNCTION email.update_timestamp();


--
-- Name: email_templates update_email_templates_timestamp; Type: TRIGGER; Schema: email; Owner: -
--

CREATE TRIGGER update_email_templates_timestamp BEFORE UPDATE ON email.email_templates FOR EACH ROW EXECUTE FUNCTION email.update_timestamp();


--
-- Name: emails update_emails_timestamp; Type: TRIGGER; Schema: email; Owner: -
--

CREATE TRIGGER update_emails_timestamp BEFORE UPDATE ON email.emails FOR EACH ROW EXECUTE FUNCTION email.update_timestamp();


--
-- Name: encryption_keys update_encryption_keys_timestamp; Type: TRIGGER; Schema: email; Owner: -
--

CREATE TRIGGER update_encryption_keys_timestamp BEFORE UPDATE ON email.encryption_keys FOR EACH ROW EXECUTE FUNCTION email.update_timestamp();


--
-- Name: attachments attachments_email_id_fkey; Type: FK CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.attachments
    ADD CONSTRAINT attachments_email_id_fkey FOREIGN KEY (email_id) REFERENCES email.emails(id) ON DELETE CASCADE;


--
-- Name: audit_log audit_log_mailbox_pubkey_fkey; Type: FK CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.audit_log
    ADD CONSTRAINT audit_log_mailbox_pubkey_fkey FOREIGN KEY (mailbox_pubkey) REFERENCES email.mailboxes(pubkey) ON DELETE SET NULL;


--
-- Name: contacts contacts_mailbox_pubkey_fkey; Type: FK CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.contacts
    ADD CONSTRAINT contacts_mailbox_pubkey_fkey FOREIGN KEY (mailbox_pubkey) REFERENCES email.mailboxes(pubkey) ON DELETE CASCADE;


--
-- Name: email_templates email_templates_mailbox_pubkey_fkey; Type: FK CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.email_templates
    ADD CONSTRAINT email_templates_mailbox_pubkey_fkey FOREIGN KEY (mailbox_pubkey) REFERENCES email.mailboxes(pubkey) ON DELETE CASCADE;


--
-- Name: emails emails_mailbox_pubkey_fkey; Type: FK CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.emails
    ADD CONSTRAINT emails_mailbox_pubkey_fkey FOREIGN KEY (mailbox_pubkey) REFERENCES email.mailboxes(pubkey) ON DELETE CASCADE;


--
-- Name: encryption_keys encryption_keys_mailbox_pubkey_fkey; Type: FK CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.encryption_keys
    ADD CONSTRAINT encryption_keys_mailbox_pubkey_fkey FOREIGN KEY (mailbox_pubkey) REFERENCES email.mailboxes(pubkey) ON DELETE CASCADE;


--
-- Name: sessions sessions_mailbox_pubkey_fkey; Type: FK CONSTRAINT; Schema: email; Owner: -
--

ALTER TABLE ONLY email.sessions
    ADD CONSTRAINT sessions_mailbox_pubkey_fkey FOREIGN KEY (mailbox_pubkey) REFERENCES email.mailboxes(pubkey) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--


