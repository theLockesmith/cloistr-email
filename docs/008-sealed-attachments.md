# RFC-008: Sealed Attachment Blobs on Blossom

**Status:** Accepted (orchestrator decision, 2026-10-29)
**Author:** coldforge
**Date:** 2026-10-29
**Related:** RFC-003 (Blossom storage), RFC-004 (Client-side crypto)

## Summary

Attachments that the server can read are encrypted before they go to Blossom. That covers mail arriving over SMTP and unencrypted-mode sends. Each blob gets its own random key, and that key is stored in Mail's database wrapped under a server key-encryption key (KEK). Uploads are signed by Mail's own Nostr service key, not by the user.

## Why

1. **Blossom GET is unauthenticated.** Anyone with a blob's hash can download it through `https://files.cloistr.xyz`. The platform rule is that anything private on Blossom is ciphertext.
2. **The old upload path signed as the user,** through a NIP-46 session held in the backend pod's memory. Those sessions exist only for the old server-side login, vanish on every pod restart, and are never present for a recipient whose mail arrives while they are offline. In practice, inbound attachments could not have been stored at all.
3. **The old code stored attachments as plaintext.** Inbound attachments and unencrypted-mode outbound attachments both went up as raw bytes.

As of this RFC, production had stored nothing on Blossom: `BLOSSOM_SERVERS` had never been set.

## Design

| Piece | Choice |
|-------|--------|
| Blob cipher | XChaCha20-Poly1305, random 24-byte nonce, AAD `cloistr-email/attachment-blob/v1` |
| Blob key | 32 random bytes per blob, never derived |
| Key storage | `email.attachments.blob_key`: the blob key sealed under the KEK (XChaCha20-Poly1305, AAD `cloistr-email/attachment-key/v1` + NUL + the attachment row id), stored as `v1.` + base64url. The row id is fixed before sealing, so a key copied onto another row (by anyone able to write the database) does not unwrap there. |
| KEK | `ATTACHMENT_KEK`, base64 of 32 random bytes, from `cloistr-email-secrets` |
| Upload signer | `MAIL_BLOSSOM_SIGNING_KEY` (hex or nsec), Mail's own key, from `cloistr-email-secrets` |
| Declared type | Always `application/octet-stream`. The real type stays in Mail's database. |
| Download | `GetAttachmentV2` fetches the blob server-side by hash, unwraps the key, decrypts and serves the file |

Code: `internal/blobseal` (sealing), `internal/email/inbound_attachments.go` (inbound), `Service.attachmentBlob` / `uploadAttachments` / `GetAttachment` (outbound and download). Migration `014_attachment_blob_key`.

### Which attachments are sealed

| Mode | What goes to Blossom | Key |
|------|----------------------|-----|
| Inbound (SMTP) | sealed by the server | `blob_key` |
| Outbound, unencrypted | sealed by the server | `blob_key` |
| Outbound, server mode | NIP-44 self-encrypted (unchanged) | none |
| Outbound, client mode (E2E) | the client's envelope, signed by the client (unchanged) | none |

### Failing closed

- With no KEK or no service key, there is **no uploader**. Inbound attachments are recorded as metadata only, and unencrypted-mode outbound attachments are not offloaded. A readable file never goes to Blossom.
- A missing, malformed or tampered key, the wrong KEK, another blob's key, or a tampered or truncated blob is an error, and **no bytes are served**.
- An unencrypted-mode row with a Blossom blob but no key is refused rather than served raw.
- Startup logs only the service public key. Key values and their parse errors are never logged.
- Attachment filenames are user content and are never logged; failure logs name the attachment row id and size.

### KEK rotation

Rotating the KEK is a rewrap: unwrap every `blob_key` with the old KEK, wrap it with the new one, and update the row. The blobs themselves never change. The `v1.` prefix leaves room for a key id if two KEKs ever need to coexist. No rotation tool exists yet.

## Accepted trade-offs and follow-ups

1. **Pooled inventory under one key.** Mail's service key owns every user's sealed blobs. Anyone who can sign as that key can list the hashes, sizes and upload times for all of them (`/list` is authenticated) and can delete them. Today that means the Mail pod, whoever can read `cloistr-email-secrets`, and Blossom admins. Contents stay opaque. **Follow-up:** per-user ownership, which needs Blossom to accept uploads on a user's behalf (option B in the orchestrator thread, 2026-10-29).
2. **The server can read these files.** This is at-rest protection against Blossom exposure, not end-to-end encryption. **Follow-up (RFC-004):** seal inbound mail to the recipient's public key with an ephemeral key, so only the client can open it.
3. **Inbound bodies are plaintext in Mail's Postgres.** This is out of scope here and has been raised with the operator.

## Blossom requirements for the service key

- Quota: 50 GiB (the configured `max_bytes`; the 1 GiB default would fill quickly).
- A rate-limit whitelist entry, because inbound bursts are all attributed to one key.
- An allowlist is not needed while Blossom enforcement is off.
- The largest single blob is about 25 MiB (the SMTP message limit) plus 40 bytes of overhead, under Blossom's 100 MiB cap.
