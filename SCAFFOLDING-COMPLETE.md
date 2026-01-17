# coldforge-email - Scaffolding Complete

## Overview

The coldforge-email service has been fully scaffolded following Coldforge standards and best practices. This document provides a quick reference for what was created and next steps.

## What Was Scaffolded

### Backend Service (Go)

**Entry Point:** `/home/forgemaster/Development/coldforge-email/cmd/email/main.go`

Complete HTTP server with:
- Health checks (`/health`, `/ready`)
- Authentication endpoints (NIP-46 challenge/verify/logout)
- Email endpoints (list, send, get, reply, delete)
- Contact endpoints (list, get, add, delete)
- Key discovery endpoints (discover, import, get mine)
- Logging and CORS middleware
- Graceful shutdown

**Internal Modules:**

| Module | Location | Purpose |
|--------|----------|---------|
| auth | `/internal/auth/` | NIP-46 authentication, Stalwart integration |
| encryption | `/internal/encryption/` | NIP-44 encryption/decryption (stub) |
| api | `/internal/api/` | REST API handlers |
| storage | `/internal/storage/` | PostgreSQL + Redis clients |
| relay | `/internal/relay/` | Nostr relay communication (stub) |
| config | `/internal/config/` | Configuration management |

### Database Schema

**Location:** `/home/forgemaster/Development/coldforge-email/configs/schema.sql`

Complete PostgreSQL schema with:
- `users` - Nostr identities and email accounts
- `emails` - Email metadata and encrypted bodies
- `contacts` - Address book
- `sessions` - Active user sessions
- `attachments` - File references (Blossom)
- `encryption_keys` - Imported keys
- `nip05_cache` - NIP-05 lookup cache
- `email_templates` - Signature/template storage
- `audit_log` - Security audit trail

Plus triggers for automatic timestamp updates.

### Frontend (TypeScript/React)

**Entry Point:** `/home/forgemaster/Development/coldforge-email/ui/src/main.tsx`

Complete SPA with:
- Login page (NIP-46 integration point)
- Inbox (email list with pagination)
- Compose (email creation with encryption option)
- Email detail view
- Contacts management
- Settings page
- Sidebar navigation
- Header with user info and logout

**Libraries:**
- React 18
- React Router v6
- TanStack React Query
- nostr-tools (for Nostr integration)
- Axios (HTTP client)
- Tailwind CSS (styling)

### Infrastructure & Deployment

**Docker:**
- `Dockerfile` - Backend multi-stage build
- `docker-compose.yml` - Local development with all services
- `ui/Dockerfile` - Frontend container

**CI/CD:**
- `.gitlab-ci.yml` - Full pipeline (test, build, deploy)

**Configuration:**
- `configs/config.example.yml` - All service options
- `configs/stalwart.toml` - Stalwart mail server setup
- `docker-compose.yml` - Service orchestration

### Documentation

| Document | Coverage |
|----------|----------|
| `README.md` | Project overview, quick start, API summary |
| `CLAUDE.md` | Development workflow, agents, quick commands |
| `docs/API.md` | Complete API endpoint reference |
| `docs/ENCRYPTION.md` | NIP-44 design, key discovery, security |
| `docs/ARCHITECTURE.md` | System components, data flows, security |
| `docs/DEPLOYMENT.md` | Local, staging, production deployment |

### Tests

**Unit Tests:** `/home/forgemaster/Development/coldforge-email/tests/unit/`
- `auth_test.go` - NIP-46 auth handler tests

**Integration Tests:** `/home/forgemaster/Development/coldforge-email/tests/integration/`
- `email_test.go` - Email sending/receiving, encryption, NIP-05

## Getting Started

### 1. Understand the Service

Read the documentation in this order:
1. `~/claude/coldforge/services/email/CLAUDE.md` - Full service requirements
2. `/home/forgemaster/Development/coldforge-email/README.md` - Quick overview
3. `/home/forgemaster/Development/coldforge-email/docs/ARCHITECTURE.md` - System design

### 2. Local Development

```bash
cd /home/forgemaster/Development/coldforge-email

# Start all services
docker-compose up

# Backend: http://localhost:8080
# Frontend: http://localhost:3001
# Stalwart Admin: http://localhost:6001
```

### 3. Run Tests

```bash
# Unit tests
go test -v ./...

# With coverage
go test -v -cover ./...

# Race detection
go test -v -race ./...

# Frontend tests
cd ui
npm install
npm test
```

### 4. Next Implementation Steps

The scaffolding is complete with stubs. To continue development:

1. **Authentication (NIP-46)**
   - Implement `internal/auth/nip46.go` methods
   - Add actual Nostr relay communication
   - Test challenge/verification flow

2. **Encryption (NIP-44)**
   - Implement `internal/encryption/nip44.go` (create file)
   - Add NIP-44 encrypt/decrypt functions
   - Integrate with nsecbunker for decryption

3. **Database Layer**
   - Replace PostgreSQL stub with actual database/sql implementation
   - Implement all CRUD operations in `internal/storage/postgres.go`
   - Run migrations on startup

4. **API Handlers**
   - Implement request/response handlers in `internal/api/`
   - Add proper error handling and validation
   - Write unit tests

5. **Frontend Integration**
   - Complete `useAuth()` hook for NIP-46 flow
   - Wire up API calls in all pages
   - Add error handling and loading states

6. **Key Discovery**
   - Implement NIP-05 lookup (DNS queries)
   - Add contacts-based key discovery
   - Cache lookup results in Redis

## Architecture Decisions

### Technology Stack

- **Backend:** Go (performance, simplicity, concurrency)
- **Frontend:** TypeScript/React (web-native, nostr-tools library)
- **Database:** PostgreSQL (ACID compliance, JSON support)
- **Cache:** Redis (session management, NIP-05 caching)
- **Mail Server:** Stalwart (SMTP/IMAP/CalDAV, extensible)

### Design Patterns

1. **NIP-46 Auth Proxy** - Converts NIP-46 to Stalwart sessions
2. **Custom Email Headers** - X-Nostr-* headers for encryption metadata
3. **Encrypted Storage** - Email bodies encrypted at rest
4. **Stateless API** - Sessions in Redis, not in-memory

### Security Model

- **No passwords** - Everything via NIP-46
- **Private keys stay in nsecbunker** - Never sent to backend
- **Email bodies encrypted** - Only recipient can decrypt
- **Metadata plaintext** - Inherent to email (to, from, subject, dates)

## File Locations

All paths are absolute for clarity:

| Item | Path |
|------|------|
| Service Code | `/home/forgemaster/Development/coldforge-email/` |
| Service Docs | `~/claude/coldforge/services/email/CLAUDE.md` |
| Coldforge Docs | `~/claude/coldforge/CLAUDE.md` |
| Atlas Role (create) | `~/Atlas/roles/kube/coldforge-email/` |
| GitLab Repo | `git@gitlab-coldforge:coldforge/coldforge-email.git` |

## Available Agents

With `.claude` symlink to Coldforge, these agents are available:

- **explore** - Research NIPs, existing code, requirements
- **reviewer** - Code review after changes
- **test-writer** - Generate tests
- **tester** - Run tests
- **debugger** - Investigate issues
- **documenter** - Update documentation
- **docker** - Create/update Dockerfiles
- **atlas-deploy** - Kubernetes deployment via Atlas
- **service-init** - Scaffold new services

## Development Workflow

1. **Before coding:**
   ```bash
   # Use explore agent
   claude explore "NIP-46 authentication flow"
   ```

2. **While coding:**
   ```bash
   # Write code, then review with agent
   claude reviewer "Implemented NIP-46 challenge creation"
   ```

3. **Testing:**
   ```bash
   # Write tests
   claude test-writer "Test NIP-46 signature verification"

   # Run tests
   claude tester "Run auth tests"
   ```

4. **Deployment:**
   ```bash
   # Create Dockerfile changes
   claude docker "Update base image to Go 1.22"

   # Setup Kubernetes
   claude atlas-deploy "Deploy coldforge-email to staging"
   ```

## Current Status

- ✅ Project structure
- ✅ Database schema
- ✅ API skeleton
- ✅ Frontend scaffolding
- ✅ Configuration templates
- ✅ Documentation (complete)
- ✅ CI/CD pipeline
- ✅ Docker setup
- ✅ Test structure

**Next:** Implement core modules (NIP-46, NIP-44, storage)

## References

- [Service Documentation](~/claude/coldforge/services/email/CLAUDE.md)
- [Coldforge Overview](~/claude/coldforge/CLAUDE.md)
- [NIP-46 (Nostr Connect)](https://github.com/nostr-protocol/nips/blob/master/46.md)
- [NIP-44 (Encryption)](https://github.com/nostr-protocol/nips/blob/master/44.md)
- [NIP-05 (Email Identity)](https://github.com/nostr-protocol/nips/blob/master/05.md)
- [Stalwart Mail](https://stalw.art/)

---

**Created:** 2026-01-17
**Scaffolding Status:** COMPLETE
**Ready for:** Implementation of core functionality
