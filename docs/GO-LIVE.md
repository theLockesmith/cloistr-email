# Go-Live Runbook — cloistr-email (multi-domain)

Serving domains: **cloistr.xyz, coldforge.xyz, aegis-hq.xyz, aegisitservices.com**

Ordered steps. Items marked **[you]** must be run by an operator against prod
infra (DNS, secrets, prod DB, deploy). Items marked **[done]** are already in
the repo.

> Honest caveats before you start:
> - Real NIP-46 bunker auth and real SMTP delivery have **never been exercised** —
>   the smoke test (step 8) is the first real run. Do it before announcing.
> - Four brand-new sending domains have **no reputation**; expect initial spam
>   foldering. Warm up slowly; don't blast volume on day one.
> - DNS propagation is the long pole — start step 2 first.

## 1. Code/config readiness — [done]
- v2 pipeline wired + encrypted round trip (e2e-verified vs live PG).
- Attachments: Blossom storage + download + MIME delivery.
- Multi-domain: `domains` table, per-domain DKIM selection, served-domains set.
- Migrations present: `configs/migrations/005,006,007` + `configs/schema.sql`.

## 2. DNS per domain — [you]  (start first; propagation lag)
For EACH of the 4 domains, generate a DKIM keypair and publish records.

```bash
# Generate DKIM keypair (writes ./dkim-keys/<domain>.{private,txt})
for d in cloistr.xyz coldforge.xyz aegis-hq.xyz aegisitservices.com; do
  ./scripts/generate-dkim-keys.sh -d "$d" -s mail
done
```

Publish per domain (values from DNS-SETUP.md; DKIM TXT from the generated .txt):
- `MX`     `<domain>.            IN MX 10 mail.<domain>.`
- `A`      `mail.<domain>.       IN A  <SMTP_INBOUND_PUBLIC_IP>`
- `SPF`    `<domain>.            IN TXT "v=spf1 mx a:mail.<domain> ~all"`   (start ~all, tighten to -all later)
- `DKIM`   `mail._domainkey.<domain>. IN TXT "v=DKIM1; k=rsa; p=<pubkey>"`
- `DMARC`  `_dmarc.<domain>.     IN TXT "v=DMARC1; p=none; rua=mailto:dmarc@<domain>"`  (p=none while warming)

Verify before relying:
```bash
for d in cloistr.xyz coldforge.xyz aegis-hq.xyz aegisitservices.com; do
  dig +short MX "$d"; dig +short TXT "$d"; dig +short TXT "mail._domainkey.$d"
done
```

## 3. Apply DB migrations — [you]  (prod DB: postgres-rw.db.coldforge.xyz)
`db.Migrate()` does NOT run migration files — apply manually. For an EXISTING db:
```bash
for m in 002_nostr_verification 003_outbound_queue 004_email_bounces \
         005_email_encryption_mode 006_widen_npub_columns 007_domains; do
  psql "$PROD_DATABASE_URL" -v ON_ERROR_STOP=1 -f configs/migrations/$m.sql
done
```
For a FRESH db: `psql "$PROD_DATABASE_URL" -f configs/schema.sql` (already includes
encryption_mode, widened npub, and the domains table).

## 4. Seed the served domains (with their DKIM keys) — [you]
Insert one row per domain; `dkim_private_key` is the PEM from step 2.
```sql
INSERT INTO domains (domain, dkim_selector, dkim_private_key, verified, active)
VALUES ('cloistr.xyz','mail', $$<PEM>$$, true, true)
ON CONFLICT (domain) DO UPDATE SET dkim_private_key=EXCLUDED.dkim_private_key,
  dkim_selector=EXCLUDED.dkim_selector, verified=true, active=true;
-- repeat for coldforge.xyz, aegis-hq.xyz, aegisitservices.com
```
The service loads these at startup → per-domain DKIM signing + served-domains set.

## 5. Secrets / config — [you]
In the cluster (coldforge namespace):
- `DATABASE_URL`, `REDIS_URL`
- `NSEC_BUNKER_RELAY_URL` (NIP-46 relay)
- `INTERNAL_API_SECRET` (cloistr-me address verification)
- `BLOSSOM_SERVERS` — confirm the prod URL (configmap currently `https://blossom.cloistr.xyz`)
- DKIM keys now live in the `domains` table, NOT the secret. `DKIM_PRIVATE_KEY`
  secret is no longer required when the table is seeded.
- `SMTP_INBOUND_DOMAINS` already lists cloistr.xyz; add the other 3 if inbound
  for them is wanted: `cloistr.xyz,coldforge.xyz,aegis-hq.xyz,aegisitservices.com`.

## 6. Deploy — [you]
GitLab CI builds on push; run the manual `deploy:atlas` job, or:
```bash
kubectl apply -k k8s/        # ATLANTIS cluster, coldforge namespace
kubectl -n coldforge rollout status deploy/cloistr-email-backend
kubectl -n coldforge logs deploy/cloistr-email-backend | grep "Multi-domain serving enabled"
```

## 7. Inbound prerequisites — [you]
Port 25 must be reachable from the internet to `mail.<domain>` (LB/NodePort +
firewall + reverse DNS / PTR on the egress IP for deliverability).

## 8. Smoke test BEFORE announcing — [you, with Claude]

**Status 2026-08-19: 3 of 5 pass. FIRST FULL ROUND-TRIP ACHIEVED** — a message
sent from cloistr.xyz to Gmail, replied to, and received back. That single
exchange exercises outbound SMTP, DKIM signing, inbound acceptance, recipient
validation, NIP-05 resolution and mailbox delivery end to end.

| # | Check | Status |
|---|-------|--------|
| 1 | NIP-46 bunker login via the API; confirm a session | **PASS** |
| 2 | For EACH domain, send to an external inbox (Gmail) | **PASS for cloistr.xyz.** aegis-hq.xyz and aegisitservices.com untested. coldforge.xyz is OUT OF SCOPE for go-live (operator's personal domain, deliberately held back). |
| 3 | Gmail "Show original": SPF=pass, DKIM=pass (d=that domain), DMARC=pass | **PASS by inference, not header-read.** cloistr.xyz publishes DMARC `p=reject`, under which an auth failure is REJECTED rather than foldered — so delivery proves authentication passed. Still worth reading the headers to confirm `d=` alignment per domain. |
| 4 | Send one with an attachment; confirm it arrives + the blob is on Blossom | **UNTESTED** |
| 5 | Reply inbound to `<you>@<domain>`; confirm it lands | **PASS** |

Optional: send to `check-auth@verifier.port25.com` or mail-tester.com per domain.

### Two bugs this gate caught, both total outages

Neither would have been found by any check short of a real send and a real reply.
Both had shipped and both looked like something milder than they were.

**Outbound (fixed 2026-08-18, `!48`).** `internal/transport/dkim.go` appended the
DKIM-Signature with a trailing CRLF that `signer.Signature()` already carries.
The doubled CRLF terminated the RFC 5322 header block early, pushing From/To/
Subject into the BODY. Gmail answered `550 5.7.1 'From' header is missing`.
Outbound mail had never worked for any domain.

**Inbound (fixed 2026-08-19, `!55`).** `cmd/email/main.go` built
`SMTPServerConfig` as a partial struct literal; `NewSMTPServer` only applied
defaults when the config was nil, so `MaxMessageSize` stayed 0 and
`if n > maxSize` rejected EVERY message with `552 5.3.4 Message too large`.
A one-line reply failed identically to a 30MB attachment, which is why it read
as a size problem rather than a total inbound outage. `ReadTimeout` and
`WriteTimeout` were also 0 — no deadlines at all, a slowloris away from
exhausting the server.

The live server now advertises `SIZE 26214400` in EHLO, which is the direct
on-the-wire proof that the limit is real.

## 9. Known gaps shipping with v1 (track, not blockers)
- Inbound attachment parsing not implemented (`inbound.go:277` TODO).
- No GC on delete (Blossom blobs leak; needs a retention/purge job).
- Attachment MIME parts not yet per-recipient encrypted (first-recipient
  simplification, same as body).
- `dkim_private_key` stored as PEM at rest — encrypt before self-serve BYO.
- cloistr-common Retry-After header bug affects 503/429 headers until dep bump.

## 10. Rollback
`kubectl -n coldforge rollout undo deploy/cloistr-email-backend`. The migrations
are additive (new column/table) and safe to leave; no data is destroyed.
