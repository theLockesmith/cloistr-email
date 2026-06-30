# RFC-004: Client-Side Crypto & Dumb Auth (Remove the Server-Side Signer)

**Status:** Accepted
**Author:** coldforge
**Date:** 2026-06-30
**Supersedes:** the server-side NIP-46 ("ModeServerSide") path from RFC-002/RFC-003
**Related:** RFC-005 (PGP Interop), cloistr-signer (FROST custody)

## Summary

Remove the signer from the mail server entirely. The Cloistr Mail backend becomes a **dumb encrypted store + SMTP transport + signature verifier**; it never holds or uses a user's key. **All user-key crypto moves client-side** (sign/encrypt outbound at send, decrypt inbound on fetch). Web-UI **auth becomes "dumb": the client signs a challenge with its own key and the backend only verifies the signature** — identical to every other Cloistr web app (shared `BackendAuthProvider`). The backend is agnostic to *how* the client signs (NIP-07 extension, NIP-46 bunker, or a raw NIP-49-protected key), which maximizes user choice.

## Motivation

The service was built so the mail server held an authorized NIP-46 session to the user's bunker and **decrypted/encrypted/signed mail on the user's behalf, server-side** (`internal/encryption/signer.go: ModeServerSide`, `EmailEncryptor` → "via the bunker"). Two problems:

1. **It defeats privacy encryption.** A server that can decrypt your "encrypted" mail is access-controlled storage, not E2E. The key holder must be the user, not the MTA.
2. **It conflated two concerns.** Logging into a *website* was wired to the same server-side bunker session the *mail crypto* used. Web-UI auth should be plain identity ("prove you're npub X, here's a token"), decoupled from any mail processing.

This also misaligned with Cloistr's stated philosophy: *client-side publishing always; the private key never leaves your device or bunker.*

## Architecture

```
            ┌─────────────────────────── CLIENT (browser) ───────────────────────────┐
            │  Holds key via: NIP-07 ext | NIP-46 bunker | raw nsec (NIP-49 at rest)  │
            │  - signs login challenge        - signs outbound (X-Nostr-Signature)    │
            │  - encrypts outbound (NIP-44)   - decrypts inbound on fetch             │
            └───────────────▲───────────────────────────────────────▲────────────────┘
                            │ signed challenge / ciphertext          │ ciphertext
            ┌───────────────┴───────────────────────────────────────┴────────────────┐
            │                         MAIL SERVER (dumb)                               │
            │  - verify signed challenge -> issue session token (no user key)          │
            │  - store ciphertext; relay SMTP; verify inbound sigs (public)            │
            │  - domain DKIM/SPF/spam (DOMAIN key, never the user key)                 │
            │  - NEVER decrypts user mail; NEVER holds a bunker session                │
            └──────────────────────────────────────────────────────────────────────────┘
```

### Crypto boundaries
- **Outbound** is user-initiated (the user is online) → the **client** signs + encrypts before submitting. The server only relays/stores.
- **Inbound, Cloistr↔Cloistr (Nostr-native sender):** sender's client NIP-44-encrypts to the recipient npub → server stores ciphertext → recipient's client decrypts on fetch. **True E2E.**
- **Inbound from the plaintext email world (Gmail, etc.):** the server unavoidably *sees* plaintext at SMTP receipt (interop reality). It then **encrypts at rest to the user's *public* key** using an ephemeral key (NIP-59-style; no access to the user's private key) so it does **not retain readable plaintext**; only the user decrypts later.
- **Domain DKIM** signing stays server-side and uses the **domain** key (per-domain `domains` table), never the user's key.

### Honest interop posture (do not over-claim)
- **Cloistr ↔ Cloistr / Nostr-native = true E2E.**
- **Cloistr ↔ outside world = TLS in transit + encrypted at rest, you control decryption — NOT E2E.**
- **Cloistr ↔ PGP world (Proton, etc.) = not interoperable today;** see RFC-005.

## Auth design (dumb, client-side; matches shared `BackendAuthProvider`)

Four endpoints. Reuse existing primitives: `internal/email/verify.go: verifySchnorr` (BIP-340) and the Redis `SessionStore`.

| Method | Path | Behavior |
|--------|------|----------|
| GET  | `/api/v1/auth/challenge`   | issue `{challenge, nonce}` (random, short-lived; stored) |
| POST | `/api/v1/auth/verify`      | body `{signedEvent}` (kind 27235): verify Schnorr sig, challenge tag matches + fresh, `created_at` recent → pubkey is the user → create session → `{token, expires_at, pubkey}` |
| POST | `/api/v1/auth/refresh`     | `Authorization: Bearer <token>` → re-issue session → `{token, expires_at}` |
| GET  | `/api/v1/auth/token-info`  | `Bearer` → `{pubkey, expires_at, valid}` |

The backend never connects to a bunker; it verifies a signature. The client uses the shared provider with `apiBase` = email's `/api`.

### Retired
- `POST /auth/nip46/challenge`, `/auth/nip46/verify`, server-side `ConnectToBunker`.
- `internal/encryption` server-side `Encryptor`-via-bunker path and `ModeServerSide`.
- The mail server's NIP-46 connection to `relay.nsecbunker.com` for auth/crypto.

## Signer choice (client-side; backend agnostic)
Because the backend only verifies a signed challenge, **all three work with zero backend changes**:
1. **NIP-07** browser extension (Alby, nos2x) — default for desktop.
2. **NIP-46** bunker (nsecbunker / cloistr-signer) — default for "key stays in bunker."
3. **Raw key ("direct")** — supported for **user freedom**, but the explicit *advanced/least-safe* path: never store a raw `nsec` in localStorage; use **NIP-49** (`ncryptsec`, passphrase-encrypted) or session-memory only; clear warning in UI.

**FROST is NOT implemented in email.** Multi-party/threshold custody of the identity key belongs to **cloistr-signer** (already has FROST) and is transparent to email — email just asks the signer to sign.

## Migration order
1. **Dumb auth (independent, ship first).** Add the 4 endpoints; wire the shared `BackendAuthProvider` in the UI; retire the server-side-bunker login. *No dependency on the encryption rework.*
2. **Encryption rework.** Outbound: client signs+encrypts at send. Inbound: store ciphertext (Cloistr-native) or encrypt-at-rest-to-pubkey (interop). Decrypt on fetch in the client. Remove the server-side encryptor.
3. **Delete dead server-side signer code** + config (NIP-46 relay, server bunker session).

## Consequences
- **Server-side body search dies.** Search becomes hybrid later (server: envelope/metadata; client: encrypted index). Acceptable — privacy ≫ search optimization.
- **Reworks recently-shipped v2 code.** The live v2 pipeline bakes in server-side encryption (`service.go` body-at-rest via bunker). Storage/Blossom/transport/multi-domain-DKIM plumbing stays; the "encrypt via server bunker" piece is removed. Net less code.
- **Interop claims must be precise** (see posture above) — a marketing/UX requirement, not just engineering.

## Out of scope
- PGP/OpenPGP interop → **RFC-005**.
- FROST / threshold custody → cloistr-signer.
- Hybrid encrypted search → future RFC.
