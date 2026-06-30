# RFC-005: PGP / OpenPGP Interop Bridge

**Status:** Draft (Proposed — not scheduled)
**Author:** coldforge
**Date:** 2026-06-30
**Related:** RFC-004 (Client-Side Crypto & Dumb Auth)

## Summary

Optional bridge so Cloistr Mail can exchange **end-to-end-encrypted** mail with the **OpenPGP email world** (ProtonMail, mailbox.org, GnuPG users, anyone publishing a PGP key). Cloistr's native encryption is **NIP-44 (secp256k1 ECDH + ChaCha20)**; the PGP world is **OpenPGP (RSA / Curve25519)**. These are different crypto ecosystems and are **not interoperable today**. This RFC scopes what real E2E interop would require. It is deliberately a **separate, opt-in workstream** — RFC-004 ships without it.

## Motivation

- A meaningful slice of the privacy-conscious target market already lives on PGP mail (Proton especially).
- "We can E2E with Proton" is a strong, concrete interop story.
- Without it, Cloistr↔PGP-world mail is only TLS-in-transit + encrypted-at-rest (per RFC-004), not E2E.

## The core problem

| | Cloistr (native) | PGP world |
|---|---|---|
| Curve | secp256k1 | Curve25519 (modern) / RSA (legacy) |
| Format | NIP-44 v2 | OpenPGP packets (RFC 4880/9580) |
| Discovery | NIP-05 / Nostr | WKD (`.well-known/openpgpkey`) / keyservers |

A Proton-encrypted message is an OpenPGP message; a NIP-44 client cannot read it, and vice-versa. Bridging requires Cloistr to **speak OpenPGP** for these conversations.

## Design options

### A. Separate per-user PGP keypair (recommended starting point)
- Generate/hold a **Curve25519 OpenPGP keypair** per user, distinct from their Nostr (secp256k1) identity.
- Publish the **public** key via **WKD** at the user's mail domain so Proton et al. encrypt to it automatically.
- Client does OpenPGP encrypt/decrypt (e.g., OpenPGP.js) for PGP-flagged conversations.
- **Pro:** works with Proton's actual stack today. **Con:** a second key identity to manage/custody; where does the PGP private key live? (Same client-side rule as RFC-004 — never server-side.)

### B. Express the Nostr secp256k1 key as an OpenPGP key
- OpenPGP ECC technically supports secp256k1, so one key could serve both worlds.
- **Con:** Proton's OpenPGP implementation does **not reliably support secp256k1** → likely won't actually interop. Tempting but probably a dead end with the dominant PGP provider. Revisit only if Proton adds support.

## Open questions
1. **PGP key custody:** client-generated + stored where? NIP-49-style passphrase? Derived deterministically from the Nostr key (so it's recoverable, not a second backup burden)?
2. **WKD hosting:** serve `.well-known/openpgpkey/...` per served domain (fits the multi-domain model). Who publishes/rotates?
3. **Inbound detection:** identify OpenPGP messages (MIME `application/pgp-encrypted`, inline `-----BEGIN PGP MESSAGE-----`) and route to the PGP decrypt path.
4. **Outbound key discovery:** look up a recipient's PGP key via WKD/keyservers; cache; fall back to plaintext+TLS when none.
5. **Client library + size:** OpenPGP.js bundle cost in the SPA; lazy-load for PGP conversations only.
6. **Signing interop:** PGP signatures vs Cloistr's X-Nostr-Signature — keep separate; don't conflate.

## Scope / sequencing
- **Not part of RFC-004.** Build only after the client-side rework lands and native E2E is solid.
- Likely phased: (1) **inbound** PGP decryption (read Proton mail) + WKD publish so others can encrypt to us; (2) **outbound** PGP encryption with recipient-key discovery.

## Non-goals
- Replacing NIP-44 native encryption (PGP is a bridge, not the default).
- Web-of-trust / keyserver operation beyond WKD lookup + publish.
