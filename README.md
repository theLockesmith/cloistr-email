# coldforge-email

Email with Nostr identity and encryption - SMTP enhanced, not replaced.

## Overview

coldforge-email is a Nostr-native email service that:

- **Authenticates via NIP-46** - Users sign in with their Nostr key through nsecbunker
- **Encrypts with NIP-44** - Email bodies can be encrypted using Nostr keypairs
- **Discovers keys via NIP-05** - Look up recipient's Nostr pubkey from their email address
- **Integrates with Stalwart** - Uses Stalwart Mail as SMTP/IMAP backbone
- **Maintains protocol cooperation** - Doesn't replace SMTP, enhances it

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│            coldforge-email Service                      │
├─────────────────────────────────────────────────────────┤
│ ┌─────────────────────────────────────────────────────┐ │
│ │ Auth Module (NIP-46)                                │ │
│ │ - NIP-46 auth proxy to Stalwart                     │ │
│ │ - Session management (Redis)                        │ │
│ │ - nsecbunker integration                            │ │
│ └─────────────────────────────────────────────────────┘ │
│ ┌─────────────────────────────────────────────────────┐ │
│ │ Encryption Module (NIP-44)                          │ │
│ │ - Email body encryption/decryption                  │ │
│ │ - Key discovery (NIP-05, contacts)                  │ │
│ │ - Custom email headers for metadata                 │ │
│ └─────────────────────────────────────────────────────┘ │
│ ┌─────────────────────────────────────────────────────┐ │
│ │ API Routes                                          │ │
│ │ - /api/v1/auth - NIP-46 authentication              │ │
│ │ - /api/v1/emails - Send/receive/encrypt             │ │
│ │ - /api/v1/keys - Key management & discovery         │ │
│ │ - /api/v1/contacts - Contact/recipient lookup       │ │
│ └─────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
         ↓ SMTP/IMAP ↓              ↓ REST ↓
┌──────────────────────┐      ┌──────────────────┐
│  Stalwart Mail       │      │  Frontend UI     │
│  (SMTP/IMAP/CalDAV)  │      │  (TypeScript)    │
└──────────────────────┘      └──────────────────┘
         ↓                              ↓
┌──────────────────────┐      ┌──────────────────┐
│  PostgreSQL          │      │  nsecbunker      │
│  (metadata)          │      │  (NIP-46 relay)  │
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
From: bob@coldforge.xyz
To: alice@coldforge.xyz
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
| Email | Receives SMTP mail | alice@coldforge.xyz |
| NIP-05 | Nostr identity | `user@coldforge.xyz` -> `npub1...` |
| Lightning | Receives payments | zaps to alice@coldforge.xyz |

## Project Structure

```
coldforge-email/
├── cmd/
│   └── email/
│       └── main.go                 # Entry point
├── internal/
│   ├── auth/                       # NIP-46 auth proxy
│   │   ├── handler.go              # HTTP handler for auth
│   │   ├── nip46.go                # NIP-46 signing requests
│   │   ├── session.go              # Session management
│   │   └── stalwart.go             # Stalwart integration
│   ├── encryption/                 # NIP-44 email encryption
│   │   ├── nip44.go                # NIP-44 encryption/decryption
│   │   ├── headers.go              # Email header handling
│   │   └── format.go               # Email body formatting
│   ├── api/                        # REST API
│   │   ├── handler.go              # Main API handler
│   │   ├── emails.go               # Email endpoints
│   │   ├── keys.go                 # Key discovery endpoints
│   │   └── contacts.go             # Contact/recipient lookup
│   ├── storage/                    # Data persistence
│   │   ├── postgres.go             # PostgreSQL client
│   │   ├── models.go               # Data models
│   │   └── migrations.go           # Database migrations
│   ├── relay/                      # Nostr relay interaction
│   │   ├── client.go               # Relay client (NIP-46)
│   │   └── events.go               # Nostr event handling
│   └── config/
│       └── config.go               # Configuration
├── ui/                             # TypeScript/React frontend
│   ├── src/
│   │   ├── components/
│   │   ├── lib/
│   │   ├── routes/
│   │   └── main.tsx
│   ├── package.json
│   └── Dockerfile
├── tests/
│   ├── unit/                       # Unit tests
│   ├── integration/                # Integration tests
│   └── fixtures/                   # Test data
├── configs/
│   ├── config.example.yml          # Configuration template
│   ├── schema.sql                  # Database schema
│   └── stalwart.toml               # Stalwart configuration
├── docs/
│   ├── API.md                      # API documentation
│   ├── ENCRYPTION.md               # Encryption design
│   ├── ARCHITECTURE.md             # Architecture details
│   └── DEPLOYMENT.md               # Deployment guide
├── .gitlab-ci.yml                  # CI/CD pipeline
├── Dockerfile                      # Backend Docker image
├── docker-compose.yml              # Local development setup
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
git clone git@gitlab-coldforge:coldforge/coldforge-email.git
cd coldforge-email

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
docker build -t coldforge-email:latest .

# Push to registry
docker tag coldforge-email:latest $REGISTRY/coldforge-email:latest
docker push $REGISTRY/coldforge-email:latest
```

## Configuration

See [configs/config.example.yml](configs/config.example.yml) for all available options.

Key environment variables:

```bash
# Database
DATABASE_URL=postgres://user:pass@localhost:5432/coldforge_email

# Cache
REDIS_URL=redis://localhost:6379

# Stalwart
STALWART_ADMIN_URL=http://stalwart:6001
STALWART_ADMIN_TOKEN=admin_token

# Nostr
NSECBUNKER_RELAY_URL=ws://localhost:4737
IDENTITY_SERVICE_URL=http://localhost:3000

# Service
LOG_LEVEL=debug
LISTEN_ADDR=0.0.0.0:8080
METRICS_ADDR=0.0.0.0:9090
```

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

### Hard Dependencies

- **Identity** (nsecbunker) - NIP-46 authentication
- **Stalwart Mail** - SMTP/IMAP/CalDAV server

### Soft Dependencies

- **Contacts** - Address book, recipient lookup
- **Files** (Blossom) - Email attachments

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

## See Also

- [Email Service Documentation](~/claude/coldforge/services/email/CLAUDE.md)
- [Coldforge Overview](~/claude/coldforge/CLAUDE.md)
- [API Documentation](docs/API.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Encryption Design](docs/ENCRYPTION.md)

## License

AGPL-3.0 - See LICENSE file
