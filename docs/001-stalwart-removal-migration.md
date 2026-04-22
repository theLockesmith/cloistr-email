# RFC-001: Remove Stalwart, Replace with Lightweight SMTP

**Status:** Complete (All Phases Done)
**Author:** coldforge
**Date:** 2026-02-04
**Updated:** 2026-02-19

## Summary

Remove Stalwart Mail Server as a dependency and replace it with `emersion/go-smtp` for both inbound and outbound email handling. This eliminates a heavyweight, underutilized dependency and gives us direct control over the mail pipeline.

## Motivation

### What we use Stalwart for today

| Capability | Used? | Details |
|-----------|-------|---------|
| SMTP submission (port 587) | Yes | Outbound email delivery |
| Admin API health check | Yes | `/healthz/live` in readiness probe |
| Account CRUD (principals API) | No | `StalwartClient` built but never called in any flow |
| IMAP/IMAPS (143/993) | No | Listeners running, nothing reads from them |
| JMAP | No | Configured, unused |
| CalDAV (5232) | No | Configured, unused |
| Spam filtering | No | Configured, unused by our code |
| Mail storage | No | Stalwart stores mail on disk; we store in PostgreSQL |

We run a full-featured mail server to use it as an SMTP relay. Everything else is dead weight.

### Problems with the current approach

1. **Operational complexity** - A full mail server with its own auth backend, storage, spam filter, and directory service runs alongside our app. Two systems to configure, monitor, and debug.

2. **Double storage** - Stalwart stores mail in its filesystem. We store mail in PostgreSQL. For incoming mail, we'd need to poll Stalwart via IMAP/JMAP and copy into our DB - a roundabout pipeline for data we could receive directly.

3. **Account sync** - The `StalwartClient` (478 lines, 27 tests) exists to keep Stalwart's principal database in sync with ours. This coordination problem disappears if we own the full stack.

4. **Impedance mismatch** - Stalwart's auth model (SQL-backed directory with its own password table) doesn't align with our Nostr-based identity system. The `stalwart.toml` config has SQL queries that join against our `users` table, creating a tight coupling at the database level.

5. **Inbound mail is blocked** - The next major feature (receiving email) requires either polling Stalwart's IMAP/JMAP or building a custom receiver. With our own SMTP server, inbound mail flows directly into our processing pipeline.

## Design

### Architecture after migration

```
                    Internet
                       |
              +--------+--------+
              |                 |
         Port 25 (in)    Port 587 (out)
              |                 |
     +--------v--------+  +----v----+
     | go-smtp Server  |  | go-smtp |
     | (inbound SMTP)  |  | Client  |
     +---------+-------+  +----+----+
               |                |
        +------v------+  +-----v------+
        | Inbound     |  | Outbound   |
        | Pipeline    |  | Pipeline   |
        |             |  |            |
        | - validate  |  | - identity |
        | - parse     |  | - encrypt  |
        | - decrypt?  |  | - format   |
        | - store     |  | - send     |
        +------+------+  +-----+------+
               |                |
               +----> PostgreSQL <----+
```

### Key components

#### 1. Outbound: SMTP Client (replaces current `SMTPTransport`)

The existing `internal/transport/smtp.go` already uses Go's `net/smtp` for the actual connection. The only Stalwart-specific aspect is the server address it connects to. Options:

- **Direct delivery via MX lookup** - resolve recipient domain, connect to their MX server, deliver directly. No relay needed.
- **External relay** - use a dedicated SMTP relay service (Postfix, or a service like Amazon SES) for deliverability reputation.
- **Both** - relay for external delivery, direct for `@cloistr.xyz` internal routing.

The `SMTPTransport` interface stays identical. We change the config to point at a relay or implement MX-based delivery.

#### 2. Inbound: SMTP Server (new)

Using `emersion/go-smtp`, implement an SMTP server that:

- Listens on port 25
- Accepts mail for `@cloistr.xyz` recipients
- Validates recipients exist in our PostgreSQL user table
- Parses the raw message (headers, body, attachments)
- Checks for `X-Nostr-*` headers (encrypted mail from other Cloistr users)
- Stores directly into PostgreSQL
- Triggers notifications (future: websocket push, Nostr DM notification)

This replaces what would have been an IMAP polling implementation.

#### 3. Mail deliverability (replaces Stalwart's built-in)

Things Stalwart handled that we need to own:

| Concern | Solution | Priority |
|---------|----------|----------|
| DKIM signing | `emersion/go-msgauth` library | P0 - required for deliverability |
| SPF | DNS TXT record (no code needed) | P0 - DNS config only |
| DMARC | DNS TXT record + reporting endpoint | P1 |
| Reverse DNS (PTR) | Infrastructure config | P0 |
| TLS for SMTP | stdlib `crypto/tls` (already in codebase) | P0 |
| Rate limiting | Middleware on inbound SMTP server | P1 |
| Spam filtering | rspamd sidecar or skip initially | P2 |
| Queue + retry | PostgreSQL-backed outbound queue | P1 |

### What gets deleted

| File/Config | Lines | Purpose | Action |
|-------------|-------|---------|--------|
| `internal/auth/stalwart.go` | 478 | Stalwart admin API client | Delete entirely |
| `tests/unit/stalwart_test.go` | ~300 | StalwartClient unit tests | Delete entirely |
| `configs/stalwart.toml` | 107 | Stalwart server config | Delete entirely |
| `docker-compose.yml` stalwart service | ~25 | Container definition | Remove service + volume |
| `config.go` Stalwart fields | 2 | `StalwartAdminURL`, `StalwartAdminToken` | Remove fields |
| `main.go` stalwart init | ~15 | Client creation + injection | Remove |
| `handler.go` stalwart field | ~10 | Stored on Handler, used in health check | Remove, update health |
| `nip46.go` stalwart field | 3 | Stored but never used | Remove parameter |
| `transport.go` comments | ~5 | "via Stalwart" references | Update comments |

### What gets added

| Component | Location | Purpose |
|-----------|----------|---------|
| SMTP server | `internal/transport/inbound.go` | Accept incoming mail on port 25 |
| Inbound pipeline | `internal/email/inbound.go` | Parse, validate, store received mail |
| DKIM signer | `internal/transport/dkim.go` | Sign outbound mail |
| Outbound queue | `internal/transport/queue.go` | Persistent send queue with retry |
| MX resolver | `internal/transport/mx.go` | DNS MX lookup for direct delivery |

### What changes

| Component | Change |
|-----------|--------|
| `SMTPTransport` | Update config to support relay OR direct MX delivery |
| `docker-compose.yml` | Remove stalwart, expose port 25 on app container |
| `config.go` | Replace Stalwart config with SMTP/DKIM config |
| `main.go` | Remove stalwart init, add SMTP server startup |
| `Handler` | Remove stalwart dependency, update health checks |
| `NIP46Handler` | Remove unused stalwart parameter |

## Migration steps

### Phase 1: Decouple (no behavior change) ✅ COMPLETE

1. ✅ Remove `stalwartClient` from `NIP46Handler` (unused)
2. ✅ Remove `stalwart` from `Handler` struct, replace health check with transport manager health
3. ✅ Remove `StalwartAdminURL`/`StalwartAdminToken` from config (make optional or remove)
4. ✅ Delete `internal/auth/stalwart.go` and its tests
5. ✅ Delete `configs/stalwart.toml`
6. ✅ Update `docker-compose.yml`: remove stalwart service, remove dependency from app service
7. ✅ Tests should still pass (stalwart client was never called in real flows)

### Phase 2: Own outbound delivery ✅ COMPLETE

1. ✅ Add DKIM signing to `SMTPTransport` using `emersion/go-msgauth`
   - Created `internal/transport/dkim.go` with RSA key parsing (PKCS#1/PKCS#8)
   - DNS TXT record generation for public key
2. ✅ Add MX resolver for direct delivery option
   - Created `internal/transport/mx.go` with caching
3. ✅ Add PostgreSQL-backed outbound queue with retry logic
   - Created `internal/transport/queue.go` with configurable retry delays
   - Added `configs/migrations/003_outbound_queue.sql`
4. ✅ Update `SMTPConfig` to support relay mode (current behavior) or direct mode
   - DeliveryMode: relay, direct, hybrid
   - LocalDomains for hybrid routing
5. ⏳ Configure DNS: SPF, DKIM, DMARC, PTR records (infrastructure task)

### Phase 3: Inbound SMTP server ✅ COMPLETE

1. ✅ Add `emersion/go-smtp` as a dependency
2. ✅ Implement SMTP server backend (`internal/transport/inbound.go`)
   - SMTPServer with configurable domains, TLS, and message size limits
   - Session handling for MAIL FROM, RCPT TO, DATA commands
   - SimpleRecipientValidator for domain-based validation
3. ✅ Implement inbound processing pipeline (`internal/email/inbound.go`)
   - ParsedMessage with full header parsing
   - Multipart MIME support (text/plain, text/html)
   - Nostr signature verification on incoming mail
   - RFC 2047 header decoding
4. ✅ Wire into `main.go` as a second listener alongside HTTP
   - Configurable via SMTP_INBOUND_ENABLED environment variable
   - Graceful shutdown support
5. ✅ InboundProcessor implements both MessageHandler and RecipientValidator
6. ✅ Update docker-compose to expose port 25
   - Added SMTP_INBOUND_* environment variables
   - Port 25 mapping (requires SMTP_INBOUND_ENABLED=true)

### Phase 4: Hardening ✅ COMPLETE

1. ✅ Add rate limiting on inbound SMTP
   - Created `internal/transport/ratelimit.go`
   - Per-IP connection and message rate tracking
   - Configurable limits: connections/minute, messages/minute, recipients/message
   - Automatic block/unblock with configurable duration
   - Whitelisted IPs bypass rate limiting
   - RateLimitError with retry-after information
2. ⏳ Add rspamd sidecar for spam filtering (deferred to future)
3. ✅ Add SPF validation for inbound mail
   - Created `internal/transport/spf.go`
   - DNS TXT record lookup and parsing
   - SPF result types: pass, fail, softfail, neutral, none, temperror, permerror
   - Configurable lookup limit and timeout
4. ✅ Add DKIM verification for inbound mail
   - Created `internal/transport/dkim_verify.go`
   - Uses emersion/go-msgauth for verification
   - Signature parsing with domain and selector extraction
   - Support for required domain validation
   - DKIMVerificationResult with per-signature details
5. ✅ Add bounce handling for outbound failures
   - Created `internal/transport/bounce.go`
   - Bounce detection via empty sender, DSN subjects, multipart/report
   - Bounce classification: hard (permanent), soft (temporary), unknown
   - Database storage for bounce tracking (email_bounces table)
   - Callbacks for hard/soft bounce events
   - Configurable cleanup of old bounce records

## New dependencies

```
github.com/emersion/go-smtp     # SMTP server + client
github.com/emersion/go-msgauth  # DKIM signing/verification
github.com/emersion/go-message  # RFC 5322 message parsing
```

These are well-maintained, widely used Go libraries by Simon Ser (emersion), who is also the author of soju (IRC bouncer) and other protocol implementations.

## Risks and mitigations

| Risk | Mitigation |
|------|-----------|
| Deliverability drops without Stalwart's reputation management | Proper DKIM/SPF/DMARC setup; consider relay for initial deployment |
| Spam on inbound without filtering | Start with basic checks (SPF, DKIM verification); add rspamd later |
| Queue reliability | PostgreSQL-backed queue with WAL guarantees; dead letter handling |
| Complexity of SMTP protocol edge cases | `emersion/go-smtp` handles protocol details; we handle business logic |
| Losing CalDAV/JMAP capabilities | We never used them. Not a loss. |

## Decision

Drop Stalwart. Build on `emersion/go-smtp`. This aligns with our architecture where PostgreSQL is the source of truth, Nostr identity is the auth layer, and the mail pipeline is fully under our control.
