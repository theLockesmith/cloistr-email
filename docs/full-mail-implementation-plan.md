# cloistr-email — Full Mail Client Implementation Plan (IMAP/POP3/SMTPS)

**Status:** Scoped 2026-06-29. Goal: make cloistr-email a fully functional mail client/server —
add IMAP (143/993), POP3 (110/995), and SMTPS/submission (465/587) to the existing inbound SMTP.

## Current architecture (as-is)
- Single Go process (`cmd/email/main.go`): HTTP API `:8080`, metrics `:9090`, inbound SMTP `:25`
  (`:2525` in-container) via `emersion/go-smtp` (`internal/transport/inbound.go`).
- Outbound: stdlib `net/smtp`, direct MX delivery, per-domain DKIM (`internal/transport/smtp.go`).
- **Storage: pure Postgres.** `emails` table stores *decomposed* fields (from/to/subject/body/html_body/
  folder/labels/nostr fields). **Raw RFC 5322 bytes are discarded** after inbound parse. Attachments →
  Blossom CDN.
- **Auth: NIP-46 only, no passwords.** Sessions in Redis.
- **Gaps:** no submission handler (587 port is dead), no IMAP, no POP3, no 465. The k8s service lists
  465/587 but the app doesn't listen on them.

## Recommended approach
Pure Go, same process, **emersion library family** (already used for SMTP):
- IMAP: `github.com/emersion/go-imap/v2` + `imapserver`, backend over Postgres.
- POP3: hand-written minimal RFC 1939 server (~250 lines; no maintained Go lib).
- SMTPS/submission: extend the existing go-smtp server with `Auth()` (SASL PLAIN) + implicit-TLS listener.
- **Reject Dovecot sidecar** (storage is Postgres, not maildir) and **JMAP** (no Go server lib, scope creep).

### Two hard design points
1. **Raw message storage** (IMAP `FETCH BODY[]` / POP3 `RETR` need full RFC 5322): add `raw_message BYTEA`
   to `emails`; inbound already has `parsed.RawMessage` — just persist it; save serialized RFC 5322 for
   sent mail too. Plus `imap_uid BIGINT` + an `imap_mailboxes` table (uid_validity/uid_next per folder).
2. **Auth for standard clients** (Thunderbird/Apple Mail/K-9 use user+password over TLS, but there are no
   passwords): add **app-specific passwords** (`user_app_passwords` table, bcrypt) — the Gmail/Fastmail
   pattern for password-less/2FA accounts. Web UI generates them; IMAP/POP/submission validate them.
3. **Encrypted bodies caveat:** `encryption_mode in (server,client)` mail is NIP-44 ciphertext — standard
   IMAP clients will see unreadable bodies. IMAP/POP serve plaintext cleanly only for `encryption_mode=none`.
   Document this; full decryption needs a smart client / proxy (separate, larger scope).

## Phased plan (~5–7 weeks, one Go engineer)
- **P1 — SMTPS submission (465+587) — ~1wk** (highest value, lowest effort): `user_app_passwords` migration
  + API to mint/revoke; `smtpSession.Auth()` (bcrypt); `SubmissionMode`/`ImplicitTLS` on `SMTPServerConfig`
  + extra listeners; wire k8s 465/587 ports.
- **P2 — Raw message storage — ~3d:** migration (`raw_message`,`imap_uid`,`imap_mailboxes`); persist raw on
  inbound + sent; UID allocation in `storage/postgres.go`.
- **P3 — IMAP (143/993) — ~2–3wk:** `internal/transport/imap.go` (Backend/Session/Mailbox over Postgres:
  Messages/Fetch/Store/Move/Expunge/Search/Flags/UIDs); STARTTLS + implicit-TLS listeners; k8s ports.
- **P4 — POP3 (110/995) — ~1wk:** minimal `internal/transport/pop3.go`; listeners; k8s ports.

## Key files
`configs/migrations/008_imap_support.sql` (new), `internal/config/config.go`,
`internal/transport/inbound.go` (Auth/SubmissionMode/ImplicitTLS), `internal/transport/imap.go` (new),
`internal/transport/pop3.go` (new), `internal/storage/postgres.go` (raw + UID + app-pw),
`internal/email/inbound.go` (persist RawMessage), `internal/transport/smtp.go` (save sent raw),
`internal/api/` (app-password endpoints), `cmd/email/main.go` (start listeners),
`k8s/services.yaml` + `k8s/backend-deployment.yaml` + `k8s/configmap.yaml`,
`Atlas/roles/kube/cloistr-email/{defaults/main.yml,shared_task_file.yml}` (new ports).

## Edge readiness (Atlas)
The nginx edge (`roles/nginx_proxy/files/stream.d/mail.conf`) already forwards `:25`/`:587` to
cloistr-email NodePorts via the api-int VIP `10.51.0.5` (OpenStack SG rules opened for the edge subnet
`10.61.0.0/16`). Add the IMAP/POP/SMTPS ports there + matching SG rules as each protocol ships.
