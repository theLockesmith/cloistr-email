# RFC-003: Blossom-First Storage Architecture

**Status:** Draft
**Author:** coldforge
**Date:** 2026-05-03
**Depends on:** RFC-002 (Nostr Email Integration)

## Summary

Move email content (bodies, attachments, optionally subjects) to Blossom distributed storage, keeping only metadata and hash references in PostgreSQL. This achieves ~85% content offload to user-controlled, decentralized storage while maintaining NIP-44 encryption for all content at rest.

## Motivation

### Current Architecture

```
┌─────────────────────────────────────────┐
│              PostgreSQL                  │
│  ┌─────────────────────────────────────┐│
│  │ emails table                        ││
│  │  - body (encrypted, stored inline)  ││
│  │  - html_body (encrypted, inline)    ││
│  │  - subject (plaintext)              ││
│  │  - metadata (from, to, timestamps)  ││
│  └─────────────────────────────────────┘│
│  ┌─────────────────────────────────────┐│
│  │ attachments table                   ││
│  │  - blossom_sha256 (reference)       ││
│  │  - blossom_url (reference)          ││
│  └─────────────────────────────────────┘│
└─────────────────────────────────────────┘
```

Problems:
1. **Single point of failure** - All content in one database
2. **Centralized control** - We hold all user data
3. **Scaling costs** - Large blobs in PostgreSQL are expensive
4. **No user sovereignty** - Users can't choose where their data lives

### Proposed Architecture

```
┌─────────────────────────────────────────┐
│              PostgreSQL                  │
│  ┌─────────────────────────────────────┐│
│  │ emails table (metadata only)        ││
│  │  - blossom_body_hash                ││
│  │  - blossom_subject_hash (optional)  ││
│  │  - encryption_nonce                 ││
│  │  - from_address, to_address         ││
│  │  - timestamps, folder, labels       ││
│  └─────────────────────────────────────┘│
└─────────────────────────────────────────┘
           │
           │ hash references
           ▼
┌─────────────────────────────────────────┐
│         Blossom Servers (User-Chosen)    │
│  ┌───────────┐ ┌───────────┐ ┌─────────┐│
│  │ Server A  │ │ Server B  │ │ Self-   ││
│  │ (public)  │ │ (public)  │ │ hosted  ││
│  └───────────┘ └───────────┘ └─────────┘│
│                                          │
│  Content (all NIP-44 encrypted):         │
│  - body.nip44 → sha256 hash              │
│  - subject.nip44 → sha256 hash           │
│  - attachments[].nip44 → sha256 hashes   │
└─────────────────────────────────────────┘
```

## Design

### Content Flow: Sending Email

```
1. User composes email (body, subject, attachments)
2. Client/server encrypts each piece with NIP-44:
   - body_encrypted = nip44_encrypt(body, recipient_pubkey)
   - subject_encrypted = nip44_encrypt(subject, recipient_pubkey)
   - attachment_encrypted = nip44_encrypt(attachment, recipient_pubkey)
3. Upload encrypted blobs to user's Blossom servers
4. Store in PostgreSQL:
   - blossom_body_hash = sha256(body_encrypted)
   - blossom_subject_hash = sha256(subject_encrypted)
   - blossom_urls = [server1/hash, server2/hash, ...]
   - encryption_nonce (for decryption)
```

### Content Flow: Reading Email

```
1. Query PostgreSQL for email metadata + hashes
2. Fetch encrypted blobs from Blossom (try servers in order)
3. Decrypt with NIP-44 using recipient's private key
4. Render email
```

### Database Schema Changes

```sql
-- Migration: Add Blossom references (NON-destructive)
-- body/html_body are KEPT (nullable) so existing inline emails still read.
-- New emails set blossom_*_hash and leave body NULL; the read path falls back
-- to the inline body whenever blossom_body_hash IS NULL (grandfathering).
ALTER TABLE emails
  ADD COLUMN blossom_body_hash VARCHAR(64),
  ADD COLUMN blossom_html_hash VARCHAR(64),
  ADD COLUMN blossom_subject_hash VARCHAR(64),  -- Optional: encrypt subject too
  ADD COLUMN blossom_servers JSONB DEFAULT '[]'::jsonb,  -- Servers the blob was stored to
  ADD COLUMN content_size_bytes BIGINT;  -- For quota tracking

CREATE INDEX idx_emails_blossom_body ON emails(blossom_body_hash);

-- NOTE: NIP-44 self-frames its nonce inside the ciphertext payload, and the
-- content address is sha256(ciphertext). No separate encryption_nonce column
-- is needed for Blossom content (the existing emails.encryption_nonce stays
-- only for legacy inline rows).
```

### User Blossom Preferences

```sql
-- New table: User's Blossom server preferences
CREATE TABLE user_blossom_prefs (
    user_id UUID PRIMARY KEY REFERENCES users(id),
    servers JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- Example: [{"url": "https://blossom.example.com", "priority": 1}]
    upload_redundancy INT DEFAULT 2,  -- Upload to N servers
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
```

### API Changes

```go
// New endpoint: Configure Blossom preferences
POST /api/v2/blossom/prefs
{
  "servers": [
    {"url": "https://blossom.example.com", "priority": 1},
    {"url": "https://my-blossom.self-hosted.com", "priority": 2}
  ],
  "upload_redundancy": 2
}

// Modified: Send email now returns Blossom hashes
POST /api/v2/email/send
Response: {
  "id": "...",
  "blossom_body_hash": "abc123...",
  "blossom_servers": ["https://..."]
}

// Modified: Get email fetches from Blossom
GET /api/v2/email/{id}
// Server fetches encrypted blob from Blossom, returns to client for decryption
```

## Security Analysis

### Threat Model

| Threat | Mitigation |
|--------|------------|
| Blossom server compromise | Content is NIP-44 encrypted; attacker sees random bytes |
| Database compromise | Only hashes and metadata; no content |
| Traffic analysis on Blossom | Servers see upload time, size, pubkey. Mitigate with Tor/proxy if needed |
| Hash = address | Anyone with hash can fetch encrypted blob. Encryption is the security layer, not obscurity |
| Blossom server unavailable | Redundant uploads to multiple servers; fallback chain |

### What Blossom Servers Learn

- Upload timestamp
- Blob size
- Uploader's Nostr pubkey (from signed upload)
- Download patterns (who fetches what, when)

They do NOT learn:
- Email content (encrypted)
- Recipient identity (unless they correlate downloads)
- Subject line (if encrypted)

### Privacy Enhancements (Optional)

1. **Anonymized uploads**: Proxy uploads through Tor
2. **Decoy traffic**: Periodic dummy uploads/downloads
3. **Server rotation**: Use different servers for different recipients
4. **Self-hosting**: Users run their own Blossom server

## Implementation Phases

### Phase 0: Blossom Client (Keystone — NOT yet built)
- No Blossom client exists in the codebase yet, and `cloistr-common` does not
  ship one. This is the foundation everything below depends on.
- Build `internal/blossom`: `Upload` (BUD-02, signed kind-24242 auth event),
  `Download` with a server-fallback chain + sha256 verification, `Delete`
  (BUD-02 GC), content addressing via sha256(ciphertext), and N-way upload
  redundancy. Unit-tested against an in-process mock Blossom server.

### Phase 1: Attachments (schema stub — wire to the real client)
- The `attachments` table already has `blossom_sha256` / `blossom_url` columns,
  but nothing uploads to Blossom yet — they are currently unpopulated stubs.
- Route attachment upload/download through the Phase 0 client (lowest-risk
  first real consumer of the client).

### Phase 2: Email Bodies
- Add `blossom_body_hash`, `blossom_html_hash` columns
- Modify send flow to upload encrypted body to Blossom
- Modify read flow to fetch and decrypt from Blossom
- Migration: Existing emails keep inline body (grandfather)

### Phase 3: Subject Lines (Optional)
- Add `blossom_subject_hash` column
- Encrypt subjects before storage
- Show "Encrypted email" in listings until opened

### Phase 4: User Blossom Preferences
- Add `user_blossom_prefs` table
- Settings UI for server configuration
- Upload redundancy controls

### Phase 5: Self-Hosting Support
- Documentation for running personal Blossom server
- Auto-discovery of user's self-hosted server via NIP-05

## Storage Distribution

After full implementation:

| Location | Content |
|----------|---------|
| **Blossom (~85%)** | Bodies, HTML, subjects, attachments (all encrypted) |
| **PostgreSQL (~15%)** | Hashes, nonces, timestamps, folders, labels, routing addresses |

## Compatibility

### Backwards Compatibility
- Existing emails with inline content continue to work
- New emails use Blossom storage
- Migration can move old content to Blossom lazily (on access)

### Interoperability
- Non-Cloistr recipients: SMTP delivery works normally (we decrypt and send plaintext)
- Cloistr-to-Cloistr: Both parties have Blossom, content stays encrypted end-to-end

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Blossom server goes away | Redundant uploads; server list is per-user configurable |
| Latency increase | Blossom fetch adds network hop; cache popular blobs in Redis |
| Complexity | Phased rollout; extensive testing |
| User confusion | Clear UI for Blossom status; fallback to inline if no servers configured |

## Success Criteria

1. Email bodies stored in Blossom (not PostgreSQL) for new emails
2. Users can configure their preferred Blossom servers
3. Email read latency < 500ms additional (compared to inline)
4. Zero content exposure in database dumps
5. Self-hosting documentation complete

## Open Questions

1. **Default Blossom servers**: Should we run public Blossom servers for users who don't configure their own?
2. **Quota management**: How do we track/limit storage when content is distributed?
3. **Garbage collection**: When an email is deleted, how do we tell Blossom servers to delete the blob?
4. **Legal compliance**: Data deletion requests when content is on third-party Blossom servers?

## References

- [Blossom Protocol (BUD-01/02/03)](https://github.com/hzrd149/blossom)
- [NIP-44: Encrypted Payloads](https://github.com/nostr-protocol/nips/blob/master/44.md)
- [RFC-002: Nostr Email Integration](002-nostr-email-integration.md)
