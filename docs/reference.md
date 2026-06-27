# coldforge-email Reference

**Comprehensive reference documentation for the SMTP + Nostr email service.**

For quick start and essential info, see [CLAUDE.md](../CLAUDE.md).

---

## Documentation Index

| Document | Content |
|----------|---------|
| [001-stalwart-removal-migration.md](001-stalwart-removal-migration.md) | RFC: Stalwart removal |
| [002-nostr-email-integration.md](002-nostr-email-integration.md) | RFC: Nostr identity layer |
| [DNS-SETUP.md](DNS-SETUP.md) | DNS configuration guide |
| [TLS-SETUP.md](TLS-SETUP.md) | TLS certificate guide |
| [nip-smtp-signing.md](nip-smtp-signing.md) | X-Nostr-* headers spec |

---

## Architecture

```
cmd/email/main.go           Server entrypoint
internal/
├── api/
│   ├── handler.go          REST API v1 endpoints
│   ├── email_handler.go    Email endpoints v2
│   └── email_types.go      API types
├── auth/
│   └── nip46.go            NIP-46 authentication
├── config/config.go        Configuration
├── email/
│   ├── service.go          Email service coordinator
│   ├── signing.go          RFC-002 email signing
│   ├── verify.go           Signature verification
│   └── inbound.go          Inbound processing
├── encryption/
│   ├── email.go            NIP-44 encryption
│   ├── nip05.go            NIP-05 key discovery
│   └── signer.go           Signer abstraction
├── identity/
│   ├── address.go          Unified address (npub ↔ email)
│   └── errors.go           Identity errors
├── metrics/metrics.go      Prometheus instrumentation
├── relays/relays.go        cloistr-common integration
├── signing/signer.go       BIP-340 Schnorr interface
├── transport/
│   ├── transport.go        Transport abstraction
│   ├── smtp.go             SMTP outbound
│   ├── inbound.go          SMTP inbound server
│   ├── dkim.go             DKIM signing
│   ├── dkim_verify.go      DKIM verification
│   ├── mx.go               MX resolver
│   ├── queue.go            Outbound queue
│   ├── ratelimit.go        Rate limiting
│   ├── spf.go              SPF validation
│   └── bounce.go           Bounce handling
└── storage/
    ├── postgres.go         PostgreSQL layer
    └── redis.go            Session store
ui/                         React frontend
k8s/                        Kubernetes manifests
```

---

## API Endpoints

### V2 Email API

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v2/email/send` | Send email (with encryption) |
| GET | `/api/v2/email/{id}` | Get email (with decryption) |
| GET | `/api/v2/email` | List emails (filtered) |
| DELETE | `/api/v2/email/{id}` | Delete email |

### V1 API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| POST | `/api/v1/auth/login` | NIP-46 login |
| GET | `/api/v1/auth/me` | Current user |
| POST | `/api/v1/auth/logout` | Logout |
| GET | `/api/v1/relays/prefs` | User relay preferences |

### Metrics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/metrics` | Prometheus metrics (port 9090) |

---

## Environment Variables

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `API_HOST` | `0.0.0.0` | Listen address |
| `API_PORT` | `8080` | Listen port |
| `DEBUG` | `false` | Debug logging |

### SMTP

| Variable | Default | Description |
|----------|---------|-------------|
| `SMTP_HOST` | - | Outbound SMTP host |
| `SMTP_PORT` | `587` | Outbound SMTP port |
| `SMTP_USER` | - | SMTP username |
| `SMTP_PASS` | - | SMTP password |
| `SMTP_INBOUND_ENABLED` | `false` | Enable inbound server |
| `SMTP_INBOUND_PORT` | `25` | Inbound listen port |
| `SMTP_DELIVERY_MODE` | `relay` | relay, direct, hybrid |

### DKIM

| Variable | Default | Description |
|----------|---------|-------------|
| `DKIM_DOMAIN` | - | Signing domain |
| `DKIM_SELECTOR` | - | DKIM selector |
| `DKIM_PRIVATE_KEY` | - | PEM private key |

### Storage

| Variable | Default | Description |
|----------|---------|-------------|
| `POSTGRES_DSN` | - | PostgreSQL connection |
| `REDIS_URL` | - | Redis URL |

### NIP-46

| Variable | Default | Description |
|----------|---------|-------------|
| `NSECBUNKER_URL` | - | Bunker connection URL |
| `NIP46_TIMEOUT` | `30s` | Auth timeout |

### Relay Preferences

| Variable | Default | Description |
|----------|---------|-------------|
| `DISCOVERY_INTERNAL` | - | Self-hosted discovery URL |
| `RELAY_LIST` | - | Comma-separated relays |
| `DISCOVERY_EXTERNAL` | - | Third-party discovery |
| `USE_CLOISTR_FALLBACK` | `true` | Use Cloistr as fallback |
| `RELAY_PREFS_CACHE_TTL` | `1h` | Cache duration |

---

## Key Architectural Decisions

### Unified Address System

Users must have a `@cloistr.xyz` address to send email:
- npub123... maps to alice@cloistr.xyz
- Consistent from-address validation
- No confusing npub addresses to recipients

### Dual Encryption Modes

| Mode | Server Access | Use Case |
|------|---------------|----------|
| NIP-46 | Has bunker access | Server encrypts/decrypts |
| NIP-07 | Zero-knowledge | Client encrypts, server stores ciphertext |

### Transport Abstraction

| Mode | Description |
|------|-------------|
| Relay | Send through configured relay server |
| Direct | Deliver via MX lookup |
| Hybrid | Direct for known domains, relay for others |

---

## Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `email_send_total` | counter | Emails sent |
| `email_receive_total` | counter | Emails received |
| `email_send_latency_seconds` | histogram | Send latency |
| `nip05_lookup_total` | counter | NIP-05 lookups |
| `nip05_cache_hits_total` | counter | Cache hits |
| `auth_sessions_active` | gauge | Active sessions |
| `http_requests_total` | counter | HTTP requests |

---

## Kubernetes Manifests

| File | Purpose |
|------|---------|
| `namespace.yaml` | Namespace definition |
| `configmap.yaml` | Non-sensitive config |
| `secret.yaml` | Secret template |
| `backend-deployment.yaml` | Backend API |
| `frontend-deployment.yaml` | Frontend UI |
| `postgres.yaml` | PostgreSQL StatefulSet |
| `redis.yaml` | Redis deployment |
| `services.yaml` | Service definitions |
| `ingress.yaml` | Ingress + Certificate |
| `hpa.yaml` | Autoscaling + PDB |
| `monitoring.yaml` | ServiceMonitor + PrometheusRules |
| `kustomization.yaml` | Kustomize config |

---

## Alerting Rules

| Alert | Description |
|-------|-------------|
| EmailSendErrorRate | Send error rate > 5% |
| SignatureVerificationFailures | Verification failures > 10/min |
| DatabaseLatency | PostgreSQL p99 > 500ms |
| SMTPConnectionFailures | Connection failures > 5/min |
| CertificateExpiration | TLS cert expires < 7 days |

---

## cloistr-common Integration

```go
import "git.aegis-hq.xyz/coldforge/cloistr-email/internal/relays"

// Create client from environment
client := relays.NewClient(logger)

// Get user's relay preferences
prefs, err := client.GetRelayPrefs(ctx, userPubkey)

// Get specific relay types
readRelays, _ := client.GetReadRelays(ctx, pubkey)
writeRelays, _ := client.GetWriteRelays(ctx, pubkey)
```

**Query Chain:** Cache → Internal Discovery → Relay List → External Discovery → Cloistr Discovery → Cloistr Relay

---

## Development History

| Phase | Features |
|-------|----------|
| RFC-001 Phase 1 | Stalwart removal |
| RFC-001 Phase 2 | Own outbound (DKIM, MX, queue) |
| RFC-001 Phase 3 | Inbound SMTP server |
| RFC-001 Phase 4 | Hardening (rate limit, SPF, DKIM verify, bounce) |
| RFC-002 Phase 1 | Email signing (BIP-340) |
| RFC-002 Phase 2 | Inbound verification, UI badges |
| RFC-002 Phase 3 | NIP proposal drafted |

---

**Last Updated:** 2026-03-11
