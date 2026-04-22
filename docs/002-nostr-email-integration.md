# RFC-002: Nostr as the Identity Layer for SMTP

**Status:** In Progress (Phases 1-3 Complete)
**Author:** coldforge
**Date:** 2026-02-04
**Updated:** 2026-02-19
**Depends on:** RFC-001 (Stalwart Removal)

## The Problem with Email Today

SMTP has no sender authentication. The `From` header is just a string - anyone can put anything there. This fundamental design flaw from 1982 is why we now have:

- **SPF** (2006) - DNS records listing which IPs can send for a domain
- **DKIM** (2007) - Domain's server signs messages; recipients verify via DNS public key
- **DMARC** (2012) - Policy layer tying SPF and DKIM together

These are **domain-level** solutions. They prove the message went through an authorized server for that domain. They do NOT prove the human who wrote the message is who they claim to be.

### Why this matters

The DKIM/SPF/DMARC system entrenches the big players:

1. **You need domain infrastructure** - DNS control, proper PTR records, key rotation
2. **Reputation systems favor incumbents** - New domains start with zero trust; Gmail's domain has decades of reputation
3. **Deliverability is gatekept** - Small senders land in spam; you practically need to use a big provider's relay to reach inboxes
4. **Complexity creates barriers** - Setting up DKIM correctly is non-trivial; most people give up and use Gmail/Outlook

The result: a handful of companies control email. Running your own mail server in 2026 means fighting an uphill battle against spam filters tuned to trust the oligopoly.

### What was tried before

**PGP/GPG signing** tried to solve this by having the *actual sender* sign messages with their personal key. It failed because:

- Key management was a nightmare (web of trust, keyservers, revocation lists)
- No good discovery mechanism (how do you find someone's public key?)
- Big providers had no incentive to implement it
- UX was terrible

**S/MIME** tried with X.509 certificates. Failed because:

- Certificate authorities are centralized gatekeepers (same problem, different flavor)
- Certificates cost money and expire
- Still no good discovery

The big players preferred DKIM/SPF/DMARC because it kept control at the infrastructure level - where they have the advantage.

## The Solution: Nostr Identity for SMTP

Nostr solves what PGP couldn't:

| PGP Problem | Nostr Solution |
|-------------|----------------|
| Key discovery | NIP-05: `alice@example.com` -> pubkey via `example.com/.well-known/nostr.json` |
| Key management | NIP-46 (remote signer), NIP-07 (browser extension) |
| Web of trust complexity | Simple: you trust a pubkey or you don't |
| Revocation | Publish a new NIP-05 mapping; old key stops resolving |
| Adoption chicken-and-egg | Nostr already has millions of users with keypairs |

### The core idea

**Sign every email with the sender's Nostr key. Verify against their NIP-05-discoverable pubkey.**

```
┌─────────────────────────────────────────────────────────────┐
│                      Email Message                          │
├─────────────────────────────────────────────────────────────┤
│ From: alice@cloistr.xyz                                   │
│ To: bob@example.com                                         │
│ Subject: Hello                                              │
│ X-Nostr-Pubkey: <alice's hex pubkey>                        │
│ X-Nostr-Sig: <schnorr signature of message hash>            │
│ X-Nostr-Signed-Headers: from;to;subject;date;message-id     │
│                                                             │
│ Message body here...                                        │
└─────────────────────────────────────────────────────────────┘
```

When Bob receives this:

1. Extract `X-Nostr-Pubkey` and `X-Nostr-Sig` headers
2. Look up `alice@cloistr.xyz` via NIP-05 (`coldforge.xyz/.well-known/nostr.json`)
3. Verify the pubkey in the email matches the NIP-05 result
4. Verify the signature against the signed headers + body
5. If valid: **cryptographic proof Alice sent this message**

No DKIM. No SPF. No DMARC. No DNS TXT records. No domain reputation. Just math.

### What this replaces

| Current System | What It Proves | Nostr Replacement | What It Proves |
|----------------|----------------|-------------------|----------------|
| SPF | Sending IP is authorized for domain | Not needed | Signature proves sender identity directly |
| DKIM | Domain's server handled the message | `X-Nostr-Sig` | Actual human authored the message |
| DMARC | Policy for SPF/DKIM failures | Not needed | Binary: signature valid or not |
| Domain reputation | "This domain historically sends good mail" | Pubkey reputation | "This identity historically sends good mail" |

### The paradigm shift

**From:** "Do I trust this domain's infrastructure?"
**To:** "Do I trust this cryptographic identity?"

This is egalitarian. A person running a mail server on a Raspberry Pi has the same cryptographic authority as Gmail. The signature is the signature. Math doesn't care about your sender volume or how long you've owned your domain.

## Technical Design

### Signing outbound email

When a Cloistr user sends an email:

```go
// Compute signature over canonical message representation
func SignEmail(msg *Message, signer Signer) error {
    // 1. Canonicalize headers (lowercase, trimmed, sorted)
    signedHeaders := []string{"from", "to", "subject", "date", "message-id"}
    canonical := canonicalizeHeaders(msg, signedHeaders)

    // 2. Add body hash
    bodyHash := sha256.Sum256([]byte(msg.Body))
    canonical = append(canonical, bodyHash[:]...)

    // 3. Sign with Nostr key (Schnorr signature)
    sig, err := signer.Sign(canonical)
    if err != nil {
        return err
    }

    // 4. Add headers
    msg.Headers["X-Nostr-Pubkey"] = signer.PublicKey()
    msg.Headers["X-Nostr-Sig"] = hex.EncodeToString(sig)
    msg.Headers["X-Nostr-Signed-Headers"] = strings.Join(signedHeaders, ";")

    return nil
}
```

### Verifying inbound email

When Cloistr receives an email with Nostr headers:

```go
func VerifyEmail(msg *Message) (*VerificationResult, error) {
    // 1. Extract Nostr headers
    pubkey := msg.Headers["X-Nostr-Pubkey"]
    sig := msg.Headers["X-Nostr-Sig"]
    signedHeaders := strings.Split(msg.Headers["X-Nostr-Signed-Headers"], ";")

    if pubkey == "" || sig == "" {
        return &VerificationResult{Signed: false}, nil
    }

    // 2. NIP-05 lookup to verify pubkey matches sender
    fromAddr := parseAddress(msg.Headers["From"])
    nip05Pubkey, err := resolveNIP05(fromAddr)
    if err != nil {
        return &VerificationResult{
            Signed: true,
            Valid: false,
            Reason: "NIP-05 lookup failed",
        }, nil
    }

    if nip05Pubkey != pubkey {
        return &VerificationResult{
            Signed: true,
            Valid: false,
            Reason: "pubkey mismatch: header vs NIP-05",
        }, nil
    }

    // 3. Reconstruct canonical message and verify signature
    canonical := canonicalizeHeaders(msg, signedHeaders)
    bodyHash := sha256.Sum256([]byte(msg.Body))
    canonical = append(canonical, bodyHash[:]...)

    valid := schnorr.Verify(pubkey, canonical, sig)

    return &VerificationResult{
        Signed: true,
        Valid: valid,
        Pubkey: pubkey,
        NIP05: fromAddr,
    }, nil
}
```

### Header specification

```
X-Nostr-Pubkey: <32-byte hex-encoded secp256k1 public key>
X-Nostr-Sig: <64-byte hex-encoded Schnorr signature>
X-Nostr-Signed-Headers: <semicolon-separated list of signed header names>
```

**Signed headers** (mandatory):
- `from` - sender address
- `to` - recipient(s)
- `date` - timestamp
- `message-id` - unique identifier

**Signed headers** (optional but recommended):
- `subject` - prevents subject tampering
- `cc`, `bcc` - if present
- `in-reply-to`, `references` - threading integrity

**Not signed** (may be modified in transit):
- `received` - added by each MTA
- `x-nostr-*` - the signature headers themselves

### Canonicalization

To ensure signatures verify correctly after transit through various MTAs:

1. Header names lowercased
2. Header values trimmed of leading/trailing whitespace
3. Line endings normalized to `\n`
4. Headers sorted alphabetically
5. Body whitespace at end of lines trimmed
6. Trailing blank lines removed

This is similar to DKIM's "relaxed" canonicalization but simpler.

### Encryption (optional, orthogonal)

Signing proves identity. Encryption protects content. They're separate concerns:

- **Signed only**: Anyone can read, but they know it's really from Alice
- **Signed + encrypted**: Only Bob can read, and he knows it's from Alice
- **Encrypted only**: Only Bob can read, but can't verify sender (not recommended)

Encryption uses NIP-44, exactly as already implemented in the codebase. The signature covers the *ciphertext*, not the plaintext - this proves Alice encrypted this specific ciphertext.

```
X-Nostr-Pubkey: <sender pubkey>
X-Nostr-Sig: <signature of encrypted body>
X-Nostr-Encrypted: true
X-Nostr-Recipient: <recipient pubkey>
X-Nostr-Algorithm: nip44

<base64-encoded NIP-44 ciphertext>
```

## Adoption Path

### Phase 1: Sign our own outbound mail ✅ COMPLETE

Every email sent from `@cloistr.xyz` includes Nostr signature headers. Recipients who understand the headers can verify; others ignore them (headers are invisible to normal email clients).

**Implementation:**
- `internal/email/signing.go` - EmailSigner with RFC-002 canonicalization
- `internal/signing/signer.go` - Signer interface with LocalSigner (BIP-340 Schnorr)
- SMTP transport auto-signs outbound emails when signer is configured
- Headers: `X-Nostr-Pubkey`, `X-Nostr-Sig`, `X-Nostr-Signed-Headers`
- Unit tests for signing (20+ tests passing)

**No recipient changes required.** We just start signing.

### Phase 2: Verify inbound mail ✅ COMPLETE

When we receive email with `X-Nostr-*` headers:
- Attempt verification
- Store verification result
- Display verification status in UI ("Verified sender" badge)

**Implementation:**
- `internal/email/verify.go` - EmailVerifier with NIP-05 cross-verification
- `internal/email/inbound.go` - InboundProcessor verifies signatures on incoming mail
- Database columns: `nostr_verified`, `nostr_verification_error`, `nostr_verified_at`
- Verification results stored per-email for UI display
- Unit tests for verification

Mail without Nostr headers works exactly as before - this is additive, not breaking.

### Phase 3: Promote the standard ✅ COMPLETE

- Document the header format as a NIP (or NIP extension)
- Encourage other Nostr-aware mail services to adopt it
- Build verification into Nostr clients that display email

**Implementation:**
- `docs/nip-smtp-signing.md` - Full NIP proposal for X-Nostr-* email headers
- Specification includes:
  - Header definitions (X-Nostr-Pubkey, X-Nostr-Sig, X-Nostr-Signed-Headers)
  - Canonicalization algorithm for headers and body
  - Signature generation using Nostr event kind 27235
  - Verification process with pseudocode
  - NIP-05 cross-verification flow
  - Security considerations (replay attacks, key rotation, mailing lists)
  - Test vectors for implementation compatibility
- Ready for submission to nostr-protocol/nips repository

### Phase 4: Leverage for spam filtering

Once we have a corpus of verified vs unverified mail:
- Verified senders get automatic trust
- Unknown pubkeys get normal spam filtering
- Pubkeys can build reputation over time (separate from domain reputation)
- Users can maintain personal "trusted pubkeys" lists

### Phase 5: Deprecate DKIM/SPF/DMARC for Nostr-verified senders

For mail between Nostr-aware systems, the old DNS-based authentication becomes redundant. We could:
- Skip DKIM signing for recipients we know verify Nostr signatures
- Ignore SPF/DKIM failures if Nostr signature is valid
- Eventually: run mail servers without any DKIM/SPF/DMARC and rely entirely on Nostr identity

## Why This Breaks the Oligopoly

### Today's barriers to running your own mail server:

1. **DKIM setup** - Generate keys, publish DNS records, rotate periodically
2. **SPF records** - Maintain list of authorized sending IPs
3. **DMARC policy** - Configure and monitor
4. **PTR records** - Need your ISP/hosting provider to set up reverse DNS
5. **IP reputation** - Fresh IPs are untrusted; takes months/years to build
6. **Domain reputation** - New domains are suspicious
7. **Feedback loops** - Need to register with major providers to handle complaints
8. **Blocklists** - One spammer on your IP range and you're blocked

### With Nostr identity:

1. **Generate a keypair** - One command, instant
2. **Publish NIP-05** - One JSON file on any web server
3. **Sign your emails** - Automatic
4. **Done**

No DNS complexity. No IP reputation. No domain age concerns. Your cryptographic identity is your reputation, and it's portable - you can move between mail servers, domains, or hosting providers without losing it.

A teenager running Postfix on a home server has the same cryptographic authority as Microsoft. The playing field is level.

## Compatibility

### With existing email

Nostr headers are just headers. Email clients that don't understand them display the message normally. The signature is invisible overhead until someone checks it.

This means:
- Gradual adoption is possible
- No flag day required
- Legacy systems continue working
- Nostr-aware systems get enhanced trust

### With DKIM/SPF/DMARC

These can coexist. A message can have both DKIM and Nostr signatures. Receivers can check either or both. Over time, as Nostr verification spreads, the DNS-based systems become less important.

### With Nostr native messaging (NIP-17)

RFC-002 originally proposed NIP-17 as a separate transport. That's still valid for Nostr-to-Nostr messaging where SMTP isn't needed. But for email interop, Nostr-signed SMTP is the bridge.

A message could even be sent *both* ways:
- NIP-17 gift-wrapped event to the recipient's relays (for Nostr clients)
- Nostr-signed SMTP email to the recipient's mailbox (for email clients)

Same content, same signature, two delivery paths.

## Open Questions

### 1. NIP-05 availability

What if the sender's domain is down when the recipient tries to verify?

Options:
- Cache NIP-05 results aggressively (we already do this)
- Include the pubkey in the header (recipient verifies signature, trusts header pubkey even if NIP-05 is unavailable)
- Defer verification until NIP-05 is reachable
- Trust-on-first-use: remember the pubkey -> email mapping

### 2. Key rotation

If Alice rotates her Nostr key, old emails verified under the old key may fail re-verification.

Options:
- NIP-05 could list historical keys with validity periods
- Store verification result at receive time (don't re-verify later)
- Accept that key rotation breaks old proofs (same as DKIM key rotation)

### 3. Replay attacks

Could someone replay a signed email to a different recipient?

Mitigations:
- Sign the `to` header (already proposed)
- Sign the `date` header and reject old messages
- Sign a unique `message-id`

### 4. Mailing lists / forwarding

When a mailing list resends a message, it typically modifies headers. This could break the signature.

Options:
- Lists preserve original signature and add their own
- Lists wrap original message as attachment/MIME part
- Define "list-safe" signed headers that survive forwarding
- Accept that forwarded messages lose original verification (DKIM has this same problem)

### 5. Should this be a NIP?

The header format should probably be standardized. Options:
- New NIP specifically for SMTP signing
- Extension to NIP-05 (add signature verification spec)
- Standalone specification outside NIP process (email-focused)

## Implementation in cloistr-email

### What we add

| Component | Location | Purpose |
|-----------|----------|---------|
| Email signer | `internal/email/signing.go` | Sign outbound messages |
| Email verifier | `internal/email/verify.go` | Verify inbound signatures |
| Verification result storage | `storage.Email` | Store verification status |
| UI badge | Frontend | Display verification status |

### What we already have

| Component | Status |
|-----------|--------|
| NIP-05 resolver | Done (`internal/encryption/nip05.go`) |
| Schnorr signing via Nostr key | Done (via `go-nostr`) |
| NIP-46 remote signer | Done (`internal/auth/nip46.go`) |
| NIP-07 client-side signing | Done (`internal/encryption/signer.go`) |
| X-Nostr-* header handling | Complete (signing + encryption headers implemented) |

### Database changes

```sql
ALTER TABLE emails ADD COLUMN nostr_verified BOOLEAN DEFAULT FALSE;
ALTER TABLE emails ADD COLUMN nostr_verification_error TEXT;
ALTER TABLE emails ADD COLUMN nostr_verified_at TIMESTAMP;
```

### Effort estimate

This is *much* simpler than implementing NIP-17/NIP-59 as a separate transport:

- Signing: ~100 lines of Go
- Verification: ~150 lines of Go
- Header canonicalization: ~50 lines
- Tests: ~200 lines
- UI changes: minimal (add a badge)

We could have this working in a day or two.

## Summary

**Don't replace SMTP. Fix it.**

Nostr provides the identity layer SMTP never had. Sign emails with your Nostr key, verify via NIP-05. No DKIM, no SPF, no DMARC, no domain reputation games.

The big players control email because they control the trust infrastructure. Nostr-signed email shifts trust from domains to cryptographic identities. Anyone with a keypair can prove who they are. The gates come down.
