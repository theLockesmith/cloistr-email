# coldforge-email Architecture

## System Overview

coldforge-email is a Nostr-native email service that integrates Nostr identity and encryption with standard SMTP/IMAP email infrastructure.

## Components

### 1. Backend Service (Go)

**Location:** `/cmd/email/main.go`, `/internal/`

**Responsibilities:**
- NIP-46 authentication proxy
- NIP-44 email encryption/decryption
- Email routing and metadata management
- Key discovery coordination
- API server

**Key Modules:**
- `auth/` - NIP-46 auth, session management, Stalwart integration
- `encryption/` - NIP-44 encryption/decryption, key handling
- `api/` - REST API endpoints
- `storage/` - Database and cache operations
- `relay/` - Nostr relay communication
- `config/` - Configuration management

### 2. Mail Server (Stalwart)

**Location:** `configs/stalwart.toml`

**Responsibilities:**
- SMTP receiving and sending
- IMAP access for clients
- User account management
- Email storage

**Integration Points:**
- Authentication via `coldforge-email` proxy
- User directory lookups via database

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
- coldforge-email backend (port 8080)
- Frontend (port 3001)

### Production Deployment

Using Atlas/Kubernetes (see `~/Atlas/roles/kube/coldforge-email/`):

1. Build Docker image: `docker build -t coldforge-email:latest .`
2. Push to registry
3. Deploy via Atlas manifests
4. Configure Stalwart for domain
5. Set up NIP-05 endpoint for email discovery

## Future Enhancements

### Phase 2: Advanced Features

- Email signing (NIP-46 signing requests)
- Subject line encryption
- Attachment encryption
- Email threading

### Phase 3: Interoperability

- IMAP client plugins for decryption
- PGP/MIME bridge for compatibility
- Thunderbird/Evolution plugins

### Phase 4: Advanced Encryption

- Group encryption (multiple recipients)
- Key rotation/recovery
- Message replies with context preservation

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

### Metrics

- Request latency
- Email send/receive rates
- Encryption/decryption success rates
- Session duration
- Error rates

### Logging

- Authentication events (login, logout, challenges)
- Email operations (send, receive, decrypt)
- API errors
- Database operations (debug level)

### Health Checks

- `/health` - Service health
- `/ready` - Dependency readiness (database, Redis, Stalwart)

## References

- [NIP-46: Nostr Connect](https://github.com/nostr-protocol/nips/blob/master/46.md)
- [NIP-44: Versioned Encryption](https://github.com/nostr-protocol/nips/blob/master/44.md)
- [NIP-05: DNS-based Identifiers](https://github.com/nostr-protocol/nips/blob/master/05.md)
- [Stalwart Mail](https://stalw.art/)
- [nsecbunker](https://github.com/kind-0/nsecbunkerd)
