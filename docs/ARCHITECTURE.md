# Cloistr Email Architecture

## System Overview

Cloistr Email is a Nostr-native email service that uses Nostr identity as the trust layer for SMTP. Instead of relying on DKIM/SPF/DMARC (domain-level authentication), emails are signed with the sender's Nostr key and verified via NIP-05.

**Key insight:** Don't replace SMTP, fix its authentication problem. Nostr provides the identity layer SMTP never had.

See [RFC-002: Nostr Identity Layer](002-nostr-email-integration.md) for the full design.

## Components

### 1. Backend Service (Go)

**Location:** `/cmd/email/main.go`, `/internal/`

**Responsibilities:**
- NIP-46/NIP-07 authentication
- Nostr signature on outbound emails
- NIP-44 email encryption/decryption
- NIP-05 key discovery
- SMTP send/receive
- Prometheus metrics

**Key Modules:**
- `auth/` - NIP-46/NIP-07 authentication, session management
- `email/` - Email service layer (coordinates all operations)
- `encryption/` - NIP-44 encryption, NIP-05 resolver
- `identity/` - Unified address system (npub ↔ email mapping)
- `transport/` - SMTP transport (planned: direct go-smtp, see RFC-001)
- `metrics/` - Prometheus instrumentation
- `api/` - REST API endpoints
- `storage/` - PostgreSQL and Redis operations
- `config/` - Configuration management

### 2. Transport Layer

**Current:** SMTP via external relay
**Planned:** Direct SMTP via `emersion/go-smtp` (see [RFC-001](001-stalwart-removal-migration.md))

The transport layer is abstracted to support:
- SMTP (current and future)
- Nostr-native messaging (NIP-17, future)
- Hybrid routing (try Nostr first, fall back to SMTP)

### 3. Frontend (TypeScript/React)

**Location:** `/ui/`

**Responsibilities:**
- User authentication via NIP-46
- Email composition and viewing
- Contact management
- Settings/preferences

**Key Libraries:**
- `nostr-tools` - Nostr protocol integration
- `@tanstack/react-query` - Server state management
- `axios` - HTTP client

### 4. Data Store (PostgreSQL)

**Location:** `configs/schema.sql`

**Tables:**
- `users` - Nostr identities and email accounts
- `emails` - Email metadata and encrypted bodies
- `contacts` - Address book
- `attachments` - File references
- `sessions` - Active sessions
- `encryption_keys` - Imported keys
- `audit_log` - Security audit trail

### 5. Cache (Redis)

**Responsibilities:**
- Session storage and TTL management
- NIP-46 challenge storage
- NIP-05 lookup caching

## Data Flow

### Authentication Flow

```
1. Frontend requests login
   ↓
2. Backend creates NIP-46 challenge
   ↓
3. Frontend sends challenge to nsecbunker
   ↓
4. User approves at nsecbunker
   ↓
5. Frontend receives signed event
   ↓
6. Frontend sends signature to backend
   ↓
7. Backend verifies signature
   ↓
8. Backend creates session + token
   ↓
9. Frontend stores token in localStorage
   ↓
10. All subsequent requests include token in Authorization header
```

### Email Sending Flow

```
1. Frontend composes email
   ↓
2. Frontend requests key discovery (NIP-05 or contacts)
   ↓
3. Backend looks up recipient's Nostr pubkey
   ↓
4. Frontend sends encrypted or plaintext to backend
   ↓
5. Backend optionally encrypts with NIP-44
   ↓
6. Backend adds custom headers (X-Nostr-Encrypted, etc.)
   ↓
7. Backend submits to Stalwart via SMTP
   ↓
8. Stalwart sends via SMTP to recipient
   ↓
9. Backend stores email metadata in database
```

### Email Receiving Flow

```
1. Stalwart receives email from sender
   ↓
2. Stalwart stores in user's mailbox
   ↓
3. Backend polls/listens for new messages
   ↓
4. Backend detects X-Nostr-Encrypted header if present
   ↓
5. Backend stores email metadata with encrypted flag
   ↓
6. Frontend requests email from backend
   ↓
7. If encrypted, backend requests decryption via NIP-46
   ↓
8. nsecbunker decrypts with user's secret key
   ↓
9. Backend returns plaintext to frontend
   ↓
10. Frontend displays email
```

## Security Architecture

### Key Custody

- **User's private key** - Stored in nsecbunker (user-controlled)
- **Session tokens** - Stored in Redis with TTL
- **Email bodies** - Encrypted in database, decrypted on request

### Authentication

- No passwords stored
- NIP-46 signing required for all operations
- Session tokens are opaque, randomly generated UUIDs
- Sessions expire after 24 hours

### Encryption

- Email bodies encrypted with NIP-44 (ChaCha20-Poly1305)
- Recipient's public key used for encryption
- Only recipient can decrypt (has private key in nsecbunker)

### Metadata

Email metadata (to, from, subject, timestamps) is NOT encrypted. This is inherent to email.

## Deployment

### Local Development

```bash
docker-compose up
```

Starts:
- PostgreSQL (port 5432)
- Redis (port 6379)
- Stalwart (ports 25, 143, 587, 993, 6001)
- Cloistr Email backend (port 8080)
- Frontend (port 3001)

### Production Deployment

Using Atlas/Kubernetes (see `~/Atlas/roles/kube/cloistr-email/`):

1. Build Docker image: `docker build -t cloistr-email:latest .`
2. Push to registry
3. Deploy via Atlas manifests
4. Configure Stalwart for domain
5. Set up NIP-05 endpoint for email discovery

## Roadmap

### Phase 1: Foundation (Current)

- [x] NIP-46/NIP-07 authentication
- [x] NIP-44 email encryption
- [x] NIP-05 key discovery
- [x] Prometheus metrics
- [x] Kubernetes deployment (Atlas)

### Phase 2: Nostr Identity Layer

Per [RFC-002](002-nostr-email-integration.md):
- [ ] Sign outbound emails with Nostr key
- [ ] Verify inbound Nostr signatures
- [ ] X-Nostr-Sig header implementation
- [ ] Signature verification UI

### Phase 3: SMTP Simplification

Per [RFC-001](001-stalwart-removal-migration.md):
- [ ] Replace Stalwart with go-smtp
- [ ] Implement inbound SMTP server
- [ ] Add DKIM signing (transitional)
- [ ] PostgreSQL-backed send queue

### Phase 4: Advanced Features

- [ ] Lightning payments for spam control
- [ ] Subject line encryption
- [ ] NIP-17 Nostr-native transport
- [ ] Group encryption

## Testing Strategy

### Unit Tests

- Auth module (NIP-46 challenges, sessions)
- Encryption module (NIP-44 encrypt/decrypt)
- API handlers (request validation, response format)

### Integration Tests

- Full email flow (send, receive, decrypt)
- NIP-05 key discovery
- Database operations
- Redis operations

### E2E Tests

- Login flow
- Compose and send email
- Receive and decrypt email
- Contact management

## Monitoring & Observability

### Prometheus Metrics

Available at `:9090/metrics`. Key metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `cloistr_email_emails_sent_total` | Counter | Emails sent by transport/status |
| `cloistr_email_email_send_duration_seconds` | Histogram | Send latency |
| `cloistr_email_nip05_lookups_total` | Counter | NIP-05 lookups (cached/success/failure) |
| `cloistr_email_nip05_cache_size` | Gauge | Cache entries |
| `cloistr_email_auth_attempts_total` | Counter | Auth attempts by method/result |
| `cloistr_email_active_sessions` | Gauge | Current sessions |
| `cloistr_email_http_requests_total` | Counter | HTTP requests by method/path/status |
| `cloistr_email_http_request_duration_seconds` | Histogram | HTTP latency |

See [DEPLOYMENT.md](DEPLOYMENT.md) for the complete metrics reference.

### Logging

- Authentication events (login, logout, challenges)
- Email operations (send, receive, decrypt)
- NIP-05 lookups
- API errors
- Database operations (debug level)

### Health Checks

- `/health` - Service liveness
- `/ready` - Dependency readiness (database, Redis)

## References

- [NIP-46: Nostr Connect](https://github.com/nostr-protocol/nips/blob/master/46.md)
- [NIP-44: Versioned Encryption](https://github.com/nostr-protocol/nips/blob/master/44.md)
- [NIP-05: DNS-based Identifiers](https://github.com/nostr-protocol/nips/blob/master/05.md)
- [Stalwart Mail](https://stalw.art/)
- [nsecbunker](https://github.com/kind-0/nsecbunkerd)
