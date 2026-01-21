# CLAUDE.md - coldforge-email

**SMTP + Nostr signing/encryption - not a bridge, an integration**

## Documentation

Full documentation: `~/claude/coldforge/services/email/CLAUDE.md`
Coldforge overview: `~/claude/coldforge/CLAUDE.md`

## Autonomous Work Mode (CRITICAL)

**Work autonomously. Do NOT stop to ask what to do next.**

- Keep working until the task is complete or you hit a genuine blocker
- Use the "Next Steps" section in the service docs to know what to work on
- Make reasonable decisions - don't ask for permission on obvious choices
- Only stop to ask if there's a true ambiguity that affects architecture
- If tests fail, fix them. If code needs review, use the reviewer agent. Keep going.
- Update this CLAUDE.md and the service docs as you make progress

## Current Status

### Completed
- [x] Project scaffolded (Go backend, React frontend)
- [x] NIP-46 authentication client implemented
  - bunker:// URL parsing
  - Challenge creation and verification
  - Relay connection and messaging
  - Session management with Redis
  - NIP-44 encrypt/decrypt via remote signer
- [x] Stalwart Mail client implemented
  - Full CRUD for principals (accounts)
  - Password, quota, email management
  - Health checks
- [x] API handlers with auth middleware
- [x] Email encryption format (NIP-44 based)
  - EmailEncryptor for encrypt/decrypt via bunker
  - Custom X-Nostr-* headers for metadata
  - RFC 5322 raw email formatting
  - Header parsing for encrypted emails
- [x] NIP-05 key discovery
  - NIP05Resolver for .well-known/nostr.json lookup
  - Caching with configurable TTL
  - CompositeKeyResolver for multiple sources
- [x] PostgreSQL database layer
  - User, Email, Contact, Attachment models
  - Full CRUD operations with pagination
  - Email filtering (direction, status, folder, labels, search)
  - Soft delete with deleted_at timestamps
  - NIP-05 cache persistence
  - Audit logging support
- [x] Transport abstraction layer
  - Swappable delivery mechanisms (SMTP, future Nostr-native)
  - Message struct for transport-agnostic representation
  - Manager for routing between transports
  - Hybrid mode: try Nostr first, fall back to SMTP
- [x] Unified address system (identity package)
  - npub ↔ email@coldforge.xyz mapping
  - Sender validation (must have unified address to send)
  - Local part validation (reserved names, format rules)
  - Recipient resolution with NIP-05 discovery
- [x] NIP-07 client-side encryption support
  - Signer abstraction (NIP-46 vs NIP-07)
  - Pre-encrypted body support in API
  - Client-side decryption flow for NIP-07 users
  - EncryptionService for coordinating modes
- [x] SMTP transport implementation
  - Full SMTP submission to Stalwart via port 587
  - STARTTLS support
  - RFC 5322 email formatting with X-Nostr-* headers
  - Per-recipient encryption support
  - Health checks
- [x] Email service layer
  - Coordinates identity, encryption, storage, and transport
  - SendRequest/SendResult for full email workflow
  - GetEmail with decryption handling (server or client-side)
  - ListEmails with filtering and pagination
- [x] V2 API endpoints for email operations
  - SendEmailV2 with encryption mode support
  - GetEmailV2 with decryption handling
  - ListEmailsV2 with filtering
  - DeleteEmailV2
- [x] Unit tests (200+ tests passing)
- [x] Frontend integration with NIP-07 support
- [x] Integration tests for email encryption flow
  - Email storage CRUD with PostgreSQL
  - Encrypted email workflow tests
  - Raw email formatting and parsing
  - NIP-05 cache integration
- [x] Docker deployment configuration
  - Production-ready Dockerfile with Go 1.24
  - nginx-based frontend serving with SPA routing
  - docker-compose with PostgreSQL, Redis, Stalwart
  - NIP-07 browser extension integration (nostr.ts)
  - LoginPage with NIP-07 and NIP-46 auth options
  - ComposePage with encryption mode selection
  - EmailPage with client-side decryption
  - InboxPage with folder tabs, search, pagination
  - Updated api.ts with v2 endpoints and types
  - useAuth hook with loginWithExtension/loginWithBunker

### Next Steps
1. Implement IMAP/JMAP receiver for incoming emails
2. Add Kubernetes deployment configuration (Atlas roles)

## Quick Commands

```bash
# Run tests
go test ./...

# Build
go build ./...

# Run locally
docker-compose up

# Build Docker image
docker build -t coldforge-email .
```

## Architecture

```
cmd/email/main.go           # Server entrypoint
internal/
├── api/
│   ├── handler.go         # REST API endpoints (v1)
│   ├── email_handler.go   # Email endpoints using full service (v2)
│   └── email_types.go     # Enhanced API types for v2 endpoints
├── auth/
│   ├── nip46.go           # NIP-46 authentication
│   └── stalwart.go        # Stalwart mail server client
├── config/config.go        # Configuration
├── email/
│   └── service.go         # Email service (coordinates all layers)
├── encryption/
│   ├── email.go           # Email encryption (NIP-44)
│   ├── nip05.go           # Key discovery (NIP-05)
│   └── signer.go          # Signer abstraction (NIP-46/NIP-07)
├── identity/
│   ├── address.go         # Unified address management
│   └── errors.go          # Identity-related errors
├── transport/
│   ├── transport.go       # Transport abstraction layer
│   └── smtp.go            # SMTP transport (Stalwart integration)
└── storage/
    ├── postgres.go         # PostgreSQL database layer
    └── redis.go            # Session store
tests/
├── unit/                   # Unit tests
│   ├── auth_test.go       # NIP-46 auth tests
│   ├── stalwart_test.go   # Stalwart client tests
│   ├── encryption_test.go # Email encryption tests
│   ├── nip05_test.go      # NIP-05 resolver tests
│   ├── postgres_test.go   # Database layer tests
│   └── transport_test.go  # Transport layer tests
└── integration/            # Integration tests
ui/                         # React frontend
```

## Key Architectural Decisions

### Unified Address System
Users must have a `@coldforge.xyz` address to send email. This ensures:
- Clear identity: npub123... maps to alice@coldforge.xyz
- No confusing npub addresses visible to recipients
- Consistent from-address validation

### Dual Encryption Modes
Support both NIP-46 (server-side) and NIP-07 (client-side):
- **NIP-46**: Server has bunker access, encrypts/decrypts on user's behalf
- **NIP-07**: Zero-knowledge mode, client encrypts before sending, server stores ciphertext
- Users can switch modes; server tracks which mode was used per message

### Transport Abstraction
The transport layer is designed for future extensibility:
- SMTP via Stalwart (current primary)
- Future Nostr-native protocol (when/if it exists)
- Hybrid mode: try Nostr first, fall back to SMTP
- Easy to add new transports without changing core logic

## Agent Usage (IMPORTANT)

**Use agents proactively. Do not wait for explicit instructions.**

| When... | Use agent... |
|---------|-------------|
| Starting new work or need context | `explore` |
| Need to research NIPs or protocols | `explore` |
| Writing or modifying code | `reviewer` after significant changes |
| Writing tests | `test-writer` |
| Running tests | `tester` |
| Investigating bugs | `debugger` |
| Updating documentation | `documenter` |
| Creating Dockerfiles | `docker` |
| Setting up Kubernetes deployment | `atlas-deploy` |
| Security-sensitive code (auth, crypto) | `security` |

## NIPs Used

| NIP | Purpose | Status |
|-----|---------|--------|
| NIP-46 | Authentication via nsecbunker | ✅ Implemented |
| NIP-44 | Email body encryption | ✅ Implemented |
| NIP-05 | Email-to-npub discovery | ✅ Implemented |
| NIP-07 | Browser extension (client-side encryption) | ✅ Implemented |
