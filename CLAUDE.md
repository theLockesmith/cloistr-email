# CLAUDE.md - coldforge-email

**SMTP + Nostr signing/encryption (Go backend, React frontend)**

**Status:** Production | **Domain:** email.cloistr.xyz

## Required Reading

| Document | Purpose |
|----------|---------|
| `~/claude/coldforge/cloistr/CLAUDE.md` | Cloistr project rules |
| [docs/reference.md](docs/reference.md) | Full API, config, architecture |
| [docs/001-stalwart-removal-migration.md](docs/001-stalwart-removal-migration.md) | RFC-001 |
| [docs/002-nostr-email-integration.md](docs/002-nostr-email-integration.md) | RFC-002 |

## Autonomous Work Mode

**Work autonomously. Do NOT stop to ask what to do next.**

- Keep working until task complete or genuine blocker
- Make reasonable decisions - don't ask permission on obvious choices
- If tests fail, fix them. Use reviewer agent. Keep going.

## Agent Usage

| When | Agent |
|------|-------|
| Starting work / need context | `explore` |
| After significant code changes | `reviewer` |
| Writing/running tests | `test-writer` / `tester` |
| Security-sensitive code | `security` |

## Quick Commands

```bash
go test ./...           # Run tests
go build ./...          # Build
docker-compose up       # Run locally
docker build -t coldforge-email .  # Docker build
```

## Project Structure

```
cmd/email/              Entry point
internal/
  api/                  REST API (v1 + v2 endpoints)
  auth/                 NIP-46 authentication
  email/                Email service, signing, verification
  encryption/           NIP-44 encryption, NIP-05 discovery
  identity/             Unified address management
  signing/              BIP-340 Schnorr signatures
  transport/            SMTP (inbound/outbound), DKIM, SPF, queue
  storage/              PostgreSQL, Redis
ui/                     React frontend
k8s/                    Kubernetes manifests
```

## Key Features

| Feature | Status |
|---------|--------|
| NIP-46 auth (server-side encryption) | Done |
| NIP-07 auth (client-side encryption) | Done |
| NIP-44 email encryption | Done |
| NIP-05 key discovery | Done |
| BIP-340 email signing | Done |
| SMTP inbound (port 25) | Done |
| SMTP outbound + DKIM | Done |
| SPF/DKIM verification | Done |
| Rate limiting | Done |
| Bounce handling | Done |
| Verified sender badges | Done |
| Relay preferences (cloistr-common) | Done |
| 200+ unit tests | Done |

## Core Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v2/email/send` | Send encrypted email |
| GET | `/api/v2/email/{id}` | Get email (with decryption) |
| GET | `/api/v2/email` | List emails (filtered) |
| DELETE | `/api/v2/email/{id}` | Delete email |
| GET | `/api/v1/relays/prefs` | User relay preferences |

## Roadmap

| Item | Priority |
|------|----------|
| Submit NIP proposal (X-Nostr-* headers) | P1 |
| Lightning spam control | P2 |
| Rspamd integration | P3 |

## Deployment

```bash
# Generate DKIM keys
./scripts/generate-dkim-keys.sh

# Deploy to Kubernetes
kubectl apply -k k8s/
```

**DNS Setup:** See [docs/DNS-SETUP.md](docs/DNS-SETUP.md)
**TLS Setup:** See [docs/TLS-SETUP.md](docs/TLS-SETUP.md)
**Atlas Role:** `~/Atlas/roles/kube/coldforge-email/`

## NIPs Used

| NIP | Purpose |
|-----|---------|
| NIP-46 | Authentication via bunker |
| NIP-44 | Email body encryption |
| NIP-05 | Email-to-npub discovery |
| NIP-07 | Browser extension signing |
| BIP-340 | Schnorr email signatures |

## See Also

- [NIP draft](docs/nip-smtp-signing.md) - X-Nostr-* email headers spec
- [cloistr-common](https://git.coldforge.xyz/coldforge/cloistr-common) - Shared libraries

---

**Last Updated:** 2026-03-11
