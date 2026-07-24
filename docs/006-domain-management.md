# 006 — Served-domain management (DKIM) + admin view handoff

**Status:** Proposed 2026-07-21. **Audience:** the session managing the admin page
(cloistr-me / cloistr-admin-ui), plus cloistr-email.
**Goal:** manage `email.domains` (served domains + per-domain DKIM) without rolling
cloistr-email pods, via an admin view — without crossing the schema-ownership
boundary or exposing DKIM private keys.

## Problem

Adding/adjusting a served domain today means editing `email.domains` in the DB and
**restarting the cloistr-email pods**. Two independent reasons:

1. **Startup-cached config.** `cmd/email/main.go` calls `db.ListActiveDomains` ONCE
   at boot, builds an in-memory `map[domain]*DKIMSigner`, installs a
   `DKIMProviderFunc` closing over that map, and calls `identity.SetServedDomains`.
   The running process never re-reads the table. A DB write alone changes nothing
   until restart.
2. **No write surface.** There is no API or UI to create/verify/activate a domain;
   `email.domains` is populated by hand. (`storage` has `ListActiveDomains`,
   `GetDomain`, `UpsertDomain` — read/upsert helpers only.)

A UI that writes the row fixes (2) but NOT (1): the live pod keeps serving its boot
snapshot. Both must be solved.

## Domain model (current)

`email.domains` (schema `email`, owned by role `cloistr_email`):

| column | notes |
|---|---|
| id | uuid |
| domain | e.g. `cloistr.xyz` |
| dkim_selector | e.g. `mail` |
| dkim_private_key | **SECRET** — PEM private key, nullable (unsigned if null) |
| verified | DNS checked out |
| active | included in the served set + signer map at boot |

## Why the admin page must NOT write `email.domains` directly

- **Schema boundary.** `email.domains` is in the `email` schema owned by
  `cloistr_email`. The admin page is cloistr-me. cloistr-email and cloistr-me are
  deliberately schema-isolated (see the identity re-key: cloistr-email holds only
  SELECT — not REFERENCES — on `public.*`). Granting cloistr-me write on the `email`
  schema reverses a boundary we just enforced.
- **Secrets.** The write includes `dkim_private_key`. Key generation and private-key
  storage must live where the signer lives (cloistr-email), behind an interface that
  never returns the private key to a UI.
- **Reload.** Only the cloistr-email process can refresh its in-memory signer map;
  the write path and the reload path want to be the same code.

## Recommended architecture: internal API + thin admin view

cloistr-email exposes an **internal domain-admin API** (Bearer `INTERNAL_API_SECRET`,
same pattern as cloistr-me's `/internal/v1/addresses/verify` that cloistr-email
already calls — just the reverse direction). It never touches the `email` schema
or exposes a private key.

**The call path is two hops, not browser-direct** (corrected 2026-07-24 by the
auth/common session). The admin UI is a browser SPA and cannot hold a bearer
secret — it would ship in the bundle:

```
browser (cloistr-admin-ui)
   --[NIP-98, platform-admin]-->  cloistr-me  /admin/v1/domains/*
   --[Bearer, server-to-server]-->  cloistr-email  /internal/v1/domains/*
```

The admin UI reuses its existing NIP-98 auth; **cloistr-me is a thin
authenticated proxy and the only thing holding the Bearer secret**. The SPA
never sees it.

The bearer is cloistr-email's **own** inbound secret — each service holds its
own, so a leak of one cannot open another's admin surface. It lives in
`cloistr-email-secrets/INTERNAL_API_SECRET` (Atlas-managed, from the
`internal_api_secret` vault var); cloistr-me is wired the same value as
`EMAIL_INTERNAL_SECRET` to make the server-to-server hop.

### Proposed endpoints (`/internal/v1/domains`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/internal/v1/domains` | List domains + status (never returns private keys) |
| POST | `/internal/v1/domains` | Register a domain: generate DKIM keypair, store private key, return the **DNS records to publish** (incl. DKIM public TXT). State = `pending_dns`. |
| POST | `/internal/v1/domains/{domain}/verify` | Check DNS (MX/SPF/DKIM/DMARC) resolves; flip `verified`. |
| POST | `/internal/v1/domains/{domain}/activate` | Set `active=true` **and refresh the live signer map** (no roll). |
| POST | `/internal/v1/domains/{domain}/deactivate` | Set `active=false` + refresh. |
| POST | `/internal/v1/domains/{domain}/rotate-dkim` | New selector+keypair, return new DNS TXT, keep old active until cutover. |

Responses expose `domain`, `dkim_selector`, `verified`, `active`, and the **public**
DKIM record — never `dkim_private_key`.

### Domain lifecycle (what the admin view renders)

```
(none) --POST--> pending_dns --publish DNS--> verify --> verified --activate--> active
                                                   ^                              |
                                                   +----------deactivate----------+
```

### Runtime reload (cloistr-email side — the actual pod-roll fix)

Replace the boot-only snapshot with a refreshable registry. Two viable shapes:

- **A. Refreshable registry (recommended).** Wrap the `signers` map + served-domains
  set in a struct with a `Reload(ctx)` that re-runs `ListActiveDomains` and atomically
  swaps the map (RWMutex). `activate`/`deactivate`/`rotate` call `Reload`. Cheap, and
  domains change rarely. Also expose reload on SIGHUP so an ops SQL edit can take
  effect without the API.
- **B. Live lookup + TTL cache.** `DKIMSignerFor` reads the DB with a short-TTL cache.
  Simpler conceptually, more per-send DB traffic; unnecessary given how rarely domains
  change. Prefer A.

Either way `identity.SetServedDomains` must move behind the same refresh (it also
gates inbound acceptance + internal/external classification).

### DKIM key + secret handling

- Generate the keypair server-side in cloistr-email (`scripts/generate-dkim-keys.sh`
  logic, in-process). Store the private key; return only the public TXT.
- Today `dkim_private_key` is a plaintext column. This is the concrete first consumer
  of the roadmap's **P2 HashiCorp Vault for DKIM keys** — worth pulling forward so the
  admin flow never persists a bare private key. At minimum, the API must guarantee the
  private key is write-only (never serialized back out).

### DNS records the view must surface (per domain)

MX → mail host; SPF (`v=spf1 ...`); DKIM (`<selector>._domainkey` TXT, public key);
DMARC (`_dmarc` TXT). DNS publishing is currently manual (no DNS-API integration).
If the Cloudflare tunnel setup exposes a DNS API we could later automate `verify`;
for now the view shows records to copy + a "verify" button that polls resolution.

## Work split

**cloistr-email (this repo):**
1. Refreshable domain/DKIM registry + `Reload` (fixes the pod-roll). *Highest value,
   ships independently of any UI.*
2. Internal domain-admin API (endpoints above) behind `INTERNAL_API_SECRET`.
3. DKIM keygen + secret storage (Vault tie-in).

**admin page (cloistr-me / cloistr-admin-ui):**
4. Domain-management view: list + status, "add domain" wizard rendering the DNS
   records, verify/activate/rotate actions — all via the internal API.

## Resolved — contract is locked (auth/common session, 2026-07-24)

1. **Internal-API-client model — confirmed.** No direct DB grant to cloistr-me on
   the `email` schema. The schema-isolation argument is decisive: cloistr-email
   holds SELECT-not-REFERENCES on `public.*`, and reversing that for a write path
   carrying a DKIM private key is backwards. Keys + signer reload stay here.
2. **Auth — two hops, not browser-direct** (see the diagram above). cloistr-me
   proxies; the SPA holds no bearer. cloistr-email mints its **own** inbound
   secret (not a reuse of cloistr-me's) and exposes it so Atlas can wire
   `EMAIL_INTERNAL_URL` + `EMAIL_INTERNAL_SECRET` into cloistr-me.
3. **Authz — admin-only (platform staff) for v1.** DKIM/DNS/served-domain
   activation has real shared-outbound-reputation blast radius. Self-serve
   BYO-domain needs ownership proof + rate limits; deferred, not needed pre-launch.
4. **Verify — poll public DNS for v1.** It is the general solution and works for
   future BYO domains in zones we do not control. A Cloudflare API token exists at
   the infra level (cert-manager uses it for DNS-01), so auto-publishing our own
   zones is a viable later enhancement — but it is not wired to these services,
   and BYO domains would not be in our CF account anyway.

**Standing constraint:** internal responses stay strictly public-only — never
serialize `dkim_private_key`, including on POST/rotate responses — so neither the
proxy nor the view can leak it. (Implemented: `DomainResponse` has no such field;
verified by review.)

## Note on urgency

Not blocking today: the only live address is `fraiyr@cloistr.xyz` and there are no
cross-domain aliases in use. This is groundwork so cross-domain aliases (see the
send-from-alias caveat: an alias on a domain with no DKIM key sends UNSIGNED) become
operable without pod rolls. Sequence #1 (reload) first — it has standalone value.
