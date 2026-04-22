# cloistr-email

Email with Nostr identity and encryption - SMTP enhanced, not replaced.

## Overview

Cloistr Email is a Nostr-native email service that:

- **Authenticates via NIP-46/NIP-07** - Users sign in with their Nostr key (nsecbunker or browser extension)
- **Signs emails with Nostr keys** - Cryptographic sender verification without DKIM/SPF/DMARC
- **Encrypts with NIP-44** - Email bodies can be encrypted using Nostr keypairs
- **Discovers keys via NIP-05** - Look up recipient's Nostr pubkey from their email address
- **Maintains protocol cooperation** - Doesn't replace SMTP, enhances it

## Strategic Direction

See the RFC documents for our architectural roadmap:

- **[RFC-001: Stalwart Removal](docs/001-stalwart-removal-migration.md)** - Replace Stalwart with lightweight `emersion/go-smtp` for direct SMTP handling
- **[RFC-002: Nostr Identity Layer](docs/002-nostr-email-integration.md)** - Use Nostr signatures as the identity layer for SMTP, eliminating the need for DKIM/SPF/DMARC

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│            Cloistr Email Service                        │
├─────────────────────────────────────────────────────────┤
│ ┌─────────────────────────────────────────────────────┐ │
│ │ Auth Module (NIP-46 / NIP-07)                       │ │
│ │ - NIP-46 remote signing via nsecbunker             │ │
│ │ - NIP-07 browser extension support                  │ │
│ │ - Session management (Redis)                        │ │
│ └─────────────────────────────────────────────────────┘ │
│ ┌─────────────────────────────────────────────────────┐ │
│ │ Email Signing & Encryption (NIP-44)                 │ │
│ │ - Nostr signature on outbound emails                │ │
│ │ - NIP-44 body encryption/decryption                 │ │
│ │ - Key discovery (NIP-05)                            │ │
│ │ - X-Nostr-* headers for verification                │ │
│ └─────────────────────────────────────────────────────┘ │
│ ┌─────────────────────────────────────────────────────┐ │
│ │ Transport Layer                                     │ │
│ │ - SMTP send/receive (go-smtp)                       │ │
│ │ - Hybrid routing (Nostr-first, SMTP fallback)       │ │
│ └─────────────────────────────────────────────────────┘ │
│ ┌─────────────────────────────────────────────────────┐ │
│ │ API Routes                                          │ │
│ │ - /api/v1/auth - Authentication                     │ │
│ │ - /api/v1/emails - Send/receive/encrypt             │ │
│ │ - /api/v1/keys - Key management & discovery         │ │
│ │ - /api/v1/contacts - Contact lookup                 │ │
│ │ - /metrics - Prometheus metrics                     │ │
│ └─────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
              ↓                              ↓
┌──────────────────────┐      ┌──────────────────┐
│  PostgreSQL          │      │  Frontend UI     │
│  (emails, users)     │      │  (React)         │
└──────────────────────┘      └──────────────────┘
              ↓                              ↓
┌──────────────────────┐      ┌──────────────────┐
│  Redis               │      │  nsecbunker      │
│  (sessions, cache)   │      │  (NIP-46 relay)  │
└──────────────────────┘      └──────────────────┘
```

## Key Features

### 1. NIP-46 Authentication

Users don't enter passwords. Instead:

1. Click "Login with Nostr"
2. Service sends NIP-46 challenge to nsecbunker
3. User approves the request (or it auto-approves if trusted)
4. Service verifies signature, creates session
5. User accesses email

**Implementation:** Auth proxy that intercepts SMTP/IMAP authentication and exchanges it for NIP-46.

### 2. Email Encryption

Emails can be encrypted using NIP-44:

```
From: bob@cloistr.xyz
To: alice@cloistr.xyz
X-Nostr-Encrypted: true
X-Nostr-Sender: npub1bob...

<NIP-44 encrypted content>
```

Decryption happens transparently when user views email (nsecbunker handles decryption).

### 3. Key Discovery

Multiple methods to find recipient's Nostr pubkey:

- **NIP-05 Lookup** - Query `.well-known/nostr.json` from recipient's domain
- **Contacts** - Already have them saved in contacts
- **Manual Entry** - User provides npub
- **Out-of-band** - Email signature, website, etc.

### 4. Unified Address

One address works everywhere:

| Protocol | Function | Example |
|----------|----------|---------|
| Email | Receives SMTP mail | alice@cloistr.xyz |
| NIP-05 | Nostr identity | `user@cloistr.xyz` -> `npub1...` |
| Lightning | Receives payments | zaps to alice@cloistr.xyz |

## Project Structure

```
cloistr-email/
├── cmd/
│   └── email/
│       └── main.go                 # Entry point
├── internal/
│   ├── api/                        # REST API handlers
│   ├── auth/                       # NIP-46/NIP-07 authentication
│   ├── config/                     # Configuration
│   ├── email/                      # Email service layer
│   ├── encryption/                 # NIP-44 encryption, NIP-05 discovery
│   ├── identity/                   # Unified address management
│   ├── metrics/                    # Prometheus instrumentation
│   ├── storage/                    # PostgreSQL and Redis
│   └── transport/                  # SMTP transport layer
├── ui/                             # React frontend
├── tests/
│   ├── unit/                       # Unit tests
│   └── integration/                # Integration tests
├── docs/
│   ├── 001-stalwart-removal-migration.md  # RFC: Stalwart removal
│   ├── 002-nostr-email-integration.md     # RFC: Nostr identity layer
│   ├── API.md                      # API documentation
│   ├── ARCHITECTURE.md             # Architecture details
│   ├── DEPLOYMENT.md               # Deployment guide
│   └── ENCRYPTION.md               # Encryption design
├── .gitlab-ci.yml                  # CI/CD pipeline
├── Dockerfile                      # Backend Docker image
├── docker-compose.yml              # Local development
├── go.mod                          # Go dependencies
└── README.md                       # This file
```

## Quick Start

### Prerequisites

- Go 1.21+
- Docker & Docker Compose
- PostgreSQL 15+
- Redis 7+

### Local Development

```bash
# Clone the repository
git clone git@gitlab-coldforge:coldforge/cloistr-email.git
cd cloistr-email

# Start services with Docker Compose
docker-compose up

# Backend will be available at http://localhost:8080
# Frontend will be available at http://localhost:3001
# Stalwart admin at http://localhost:6001
```

### Run Tests

```bash
# All tests
go test -v ./...

# With coverage
go test -v -cover ./...

# With race detection
go test -v -race ./...
```

### Build Docker Image

```bash
# Local build
docker build -t cloistr-email:latest .

# Push to registry
docker tag cloistr-email:latest $REGISTRY/cloistr-email:latest
docker push $REGISTRY/cloistr-email:latest
```

## Configuration

See [configs/config.example.yml](configs/config.example.yml) for all available options.

Key environment variables:

```bash
# Database
DATABASE_URL=postgres://user:pass@localhost:5432/cloistr_email

# Cache
REDIS_URL=redis://localhost:6379

# Nostr
NSECBUNKER_RELAY_URL=wss://relay.nsecbunker.com

# Service
LOG_LEVEL=debug
LISTEN_ADDR=0.0.0.0:8080
METRICS_ADDR=0.0.0.0:9090
```

## Monitoring

Prometheus metrics available at `:9090/metrics`. See [DEPLOYMENT.md](docs/DEPLOYMENT.md) for the full metrics reference.

Key metrics:
- `cloistr_email_emails_sent_total` - Email send counts by transport/status
- `cloistr_email_nip05_lookups_total` - NIP-05 lookup results
- `cloistr_email_auth_attempts_total` - Authentication attempts
- `cloistr_email_http_requests_total` - HTTP request counts

## API Endpoints

### Authentication

- `POST /api/v1/auth/nip46/challenge` - Start NIP-46 auth
- `POST /api/v1/auth/nip46/verify` - Verify NIP-46 signature
- `POST /api/v1/auth/logout` - Logout session

### Emails

- `GET /api/v1/emails` - List emails
- `POST /api/v1/emails` - Send email
- `GET /api/v1/emails/{id}` - Get email details
- `POST /api/v1/emails/{id}/reply` - Reply to email
- `DELETE /api/v1/emails/{id}` - Delete email

### Keys & Discovery

- `GET /api/v1/keys/discover` - Look up recipient's Nostr key
- `POST /api/v1/keys/import` - Import a key
- `GET /api/v1/keys/mine` - Get current user's key info

### Contacts

- `GET /api/v1/contacts` - List contacts
- `POST /api/v1/contacts` - Add contact
- `GET /api/v1/contacts/{id}` - Get contact
- `DELETE /api/v1/contacts/{id}` - Remove contact

## Dependencies

### Required

- **PostgreSQL** - Email storage, user accounts, NIP-05 cache
- **Redis** - Session management, rate limiting
- **nsecbunker** (or NIP-07 extension) - Nostr key signing

### Optional

- **Blossom** (coldforge-files) - Email attachments
- **Grafana** - Metrics visualization

## Testing

Tests are organized by type:

- **Unit tests** (`tests/unit/`) - Test individual modules
- **Integration tests** (`tests/integration/`) - Test service interactions
- **Fixtures** (`tests/fixtures/`) - Shared test data

Run with:

```bash
# Just unit tests
go test -v ./tests/unit/...

# Just integration tests (requires services running)
go test -v ./tests/integration/...

# All tests
go test -v ./...
```

## Development Workflow

1. **Before coding:** Read `~/claude/coldforge/services/email/CLAUDE.md`
2. **While coding:** Implement features, write tests alongside
3. **Before commit:** Run `go test ./...` and ensure tests pass
4. **Code review:** Use `reviewer` agent for significant changes
5. **Before merging:** Ensure CI/CD passes

## Documentation

- [API Documentation](docs/API.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Encryption Design](docs/ENCRYPTION.md)
- [Deployment Guide](docs/DEPLOYMENT.md)
- [RFC-001: Stalwart Removal](docs/001-stalwart-removal-migration.md)
- [RFC-002: Nostr Identity Layer](docs/002-nostr-email-integration.md)

## License

AGPL-3.0 - See LICENSE file
