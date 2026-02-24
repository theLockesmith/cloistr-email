# NIP-XX: Nostr-Signed SMTP Email

`draft` `optional`

## Abstract

This NIP defines a standard for signing SMTP email messages with Nostr keys, enabling cryptographic sender verification that is independent of domain-based trust systems (SPF, DKIM, DMARC). Recipients can verify that an email was authored by the holder of a specific Nostr keypair and cross-reference the sender's identity via NIP-05.

The specification supports multiple signatures, enabling organizational attestation where both an individual author and their organization can sign a message.

## Motivation

SMTP email lacks native sender authentication. The `From` header is a free-form string that anyone can set to any value. Modern email security relies on domain-level solutions:

- **SPF**: DNS records listing authorized sending IPs for a domain
- **DKIM**: Domain server signs messages; recipients verify via DNS public key
- **DMARC**: Policy layer tying SPF and DKIM together

These systems authenticate domains, not individuals. They prove a message passed through authorized infrastructure, not that a specific person wrote it. This design entrenches large email providers who have established domain reputation and infrastructure.

Nostr provides what email lacks:

| Problem | Nostr Solution |
|---------|----------------|
| Sender authentication | Schnorr signatures with secp256k1 keys |
| Key discovery | NIP-05 maps identifiers to public keys |
| Key management | NIP-07 (browser extensions), NIP-46 (remote signers) |
| Decentralized identity | No certificate authorities or central registries |

By signing emails with Nostr keys, we enable:

1. **Cryptographic proof of authorship** - The person holding the private key wrote this message
2. **Cross-platform identity** - Same key used for Nostr posts and email
3. **Egalitarian trust** - A home server has the same cryptographic authority as Gmail
4. **Organizational attestation** - Companies can counter-sign employee emails
5. **Gradual adoption** - Headers are ignored by non-aware clients

### Comparison to SPF/DKIM/DMARC

Nostr signatures can serve as a complete replacement for traditional email authentication:

| Traditional | Nostr Equivalent | Mechanism |
|-------------|------------------|-----------|
| **SPF** | NIP-05 | Domain's `nostr.json` lists authorized pubkeys for each address |
| **DKIM** | X-Nostr-Signature | Pubkey signs message; signature in header; no DNS lookup needed |
| **DMARC** | NIP-05 alignment | Verify signature pubkey matches the From address's NIP-05 entry |
| **Domain DKIM** | Org counter-signature | Organization key counter-signs author signature |

Key advantages over traditional systems:
- **Individual-level authentication**: DKIM proves a server handled the message; Nostr proves the author wrote it
- **No DNS dependency for verification**: Signature verification works offline; NIP-05 is optional enhancement
- **Egalitarian**: Personal mail server has same cryptographic authority as corporate infrastructure
- **Portable identity**: Key works across email, Nostr, and any other system using secp256k1

## Specification

### Header Format

Signatures are encoded in `X-Nostr-Signature` headers using a semicolon-separated key=value format, similar to DKIM:

```
X-Nostr-Signature: p=<pubkey>;s=<signature>;h=<signed-headers>;r=<role>
```

**Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `p` | Yes | 32-byte hex-encoded secp256k1 public key (64 characters) |
| `s` | Yes | 64-byte hex-encoded BIP-340 Schnorr signature (128 characters) |
| `h` | Yes | Semicolon-separated list of signed header names (lowercase) |
| `r` | No | Signer role: `author`, `organization`, `gateway`, or custom value |

**Example (single signature):**
```
X-Nostr-Signature: p=7e7e9c42a91bfef19fa929e5fda1b72e0ebc1a4c1141673e2794234d86addf4e;s=...128 hex chars...;h=date;from;message-id;subject;to;r=author
```

**Example (author + organization):**
```
X-Nostr-Signature: p=7e7e9c42a91bfef19fa929e5fda1b72e0ebc1a4c1141673e2794234d86addf4e;s=...;h=date;from;message-id;subject;to;r=author
X-Nostr-Signature: p=a1b2c3d4e5f6...;s=...;h=date;from;x-nostr-signature;r=organization
```

Multiple `X-Nostr-Signature` headers are allowed, following the same pattern as multiple DKIM-Signature headers in RFC 6376.

### Signer Roles

The `r=` parameter indicates the signer's relationship to the message:

| Role | Description | Signs Over |
|------|-------------|------------|
| `author` | The individual who wrote the message | Message content and headers |
| `organization` | Organizational attestation (employer, domain owner) | Author signature + selected headers |
| `gateway` | Mail gateway or relay adding attestation | Previous signatures + transport headers |
| (custom) | Application-specific roles | Implementation-defined |

**Role semantics:**
- `author` signature SHOULD be present on all signed messages
- `organization` signature indicates "this organization vouches for this sender"
- `gateway` signature indicates the message passed through a trusted relay
- Multiple signatures of the same role are allowed (e.g., multiple organizations)

### Mandatory Signed Headers

The following headers MUST be signed when present:

| Header | Purpose |
|--------|---------|
| `from` | Sender identity - prevents spoofing |
| `to` | Recipients - prevents forwarding attacks |
| `date` | Timestamp - prevents replay attacks |
| `message-id` | Unique identifier - ensures signature uniqueness |

### Optional Signed Headers

The following headers SHOULD be signed when present:

| Header | Purpose |
|--------|---------|
| `subject` | Prevents subject tampering |
| `cc` | Additional recipients |
| `in-reply-to` | Threading integrity |
| `references` | Threading integrity |

### Headers That MUST NOT Be Signed

| Header | Reason |
|--------|--------|
| `received` | Added by each mail transfer agent |
| `return-path` | Modified during delivery |
| `authentication-results` | Added by receiving server |

### Counter-Signature Requirements

When signing as `organization` or `gateway` (counter-signing):

1. The signed headers (`h=`) MUST include `x-nostr-signature`
2. This creates a cryptographic chain - the counter-signature covers the author signature
3. Counter-signatures SHOULD also include `date` and `from` for context

**Example counter-signature:**
```
X-Nostr-Signature: p=<org_pubkey>;s=<org_sig>;h=date;from;x-nostr-signature;r=organization
```

This proves: "Organization X vouches that the author signature is legitimate and the author is authorized to send on behalf of this domain."

### Canonicalization Algorithm

To ensure signatures verify correctly after transit through various mail transfer agents, both signing and verification MUST use the following canonicalization:

#### Header Canonicalization

1. Convert header names to lowercase
2. Trim leading and trailing whitespace from header values
3. Replace all `\r\n` sequences with `\n`
4. Replace all standalone `\r` with `\n`
5. Sort headers alphabetically by name
6. Format as `name:value` pairs joined by `\n`

**For X-Nostr-Signature headers when counter-signing:**
- Include the full header value (all parameters)
- If multiple X-Nostr-Signature headers exist, concatenate them in order of appearance

#### Body Canonicalization

1. Replace all `\r\n` sequences with `\n`
2. Replace all standalone `\r` with `\n`
3. Trim trailing whitespace (spaces and tabs) from each line
4. Remove all trailing blank lines

#### Canonical Data Construction

```
canonical_headers = join(sorted_headers, "\n")
body_hash = SHA256(canonical_body)
canonical_data = canonical_headers + "\n" + hex(body_hash)
```

The signature is computed over `SHA256(canonical_data)`.

### Signature Generation

The signature uses a Nostr event structure to leverage existing BIP-340 Schnorr signing implementations:

```python
def sign_email(canonical_data, private_key, role="author"):
    message_hash = sha256(canonical_data)

    # Create deterministic Nostr event
    event = {
        "pubkey": derive_pubkey(private_key),
        "created_at": 0,  # Fixed for determinism
        "kind": 27235,    # Email signature event kind
        "tags": [],
        "content": hex(message_hash)
    }

    # Compute event ID and sign
    event["id"] = sha256(serialize_event(event))
    event["sig"] = schnorr_sign(event["id"], private_key)

    return event["sig"]
```

**Event Kind 27235** is reserved for email signature events. The fixed `created_at` of 0 ensures verification can reconstruct the exact event ID.

### Verification Process

```python
def verify_email(headers, body):
    results = []

    # Process each X-Nostr-Signature header
    for sig_header in headers.get_all("X-Nostr-Signature"):
        params = parse_signature_header(sig_header)

        pubkey = params["p"]
        sig = params["s"]
        signed_headers = params["h"].split(";")
        role = params.get("r", "author")

        # Validate formats
        if len(pubkey) != 64 or not is_hex(pubkey):
            results.append(VerificationResult(valid=False, reason="invalid pubkey"))
            continue

        if len(sig) != 128 or not is_hex(sig):
            results.append(VerificationResult(valid=False, reason="invalid signature"))
            continue

        # Reconstruct canonical data
        canonical = canonicalize(headers, body, signed_headers)
        message_hash = sha256(canonical)

        # Reconstruct the Nostr event
        event = {
            "pubkey": pubkey,
            "created_at": 0,
            "kind": 27235,
            "tags": [],
            "content": hex(message_hash),
            "sig": sig
        }
        event["id"] = sha256(serialize_event(event))

        # Verify Schnorr signature
        valid = schnorr_verify(pubkey, event["id"], sig)

        results.append(VerificationResult(
            valid=valid,
            pubkey=pubkey,
            role=role,
            signed_headers=signed_headers
        ))

    return results
```

### NIP-05 Cross-Verification

After signature verification succeeds, implementations SHOULD cross-reference pubkeys against NIP-05 identities:

**For author signatures:**
1. Extract the email address from the `From` header
2. Perform NIP-05 lookup: `GET https://<domain>/.well-known/nostr.json?name=<local-part>`
3. Compare the returned pubkey with the author's `p=` value

**For organization signatures:**
1. Look up the organization's well-known pubkey via NIP-05 (e.g., `_@domain.com` or `org@domain.com`)
2. Verify the organization pubkey matches the counter-signature's `p=` value

```python
def cross_verify_nip05(from_address, author_pubkey, org_pubkey=None):
    email = extract_email(from_address)
    local_part, domain = email.split("@")

    # Verify author
    response = fetch(f"https://{domain}/.well-known/nostr.json?name={local_part}")
    author_verified = response["names"].get(local_part) == author_pubkey

    # Verify organization (if present)
    org_verified = None
    if org_pubkey:
        # Convention: organization key at "_" or "org"
        org_nip05 = response["names"].get("_") or response["names"].get("org")
        org_verified = org_nip05 == org_pubkey

    return NIP05Result(
        author_verified=author_verified,
        org_verified=org_verified
    )
```

**Trust Levels:**

| Author Sig | Org Sig | NIP-05 Author | NIP-05 Org | Trust Level |
|------------|---------|---------------|------------|-------------|
| Valid | Valid | Match | Match | Fully verified - individual and organization confirmed |
| Valid | Valid | Match | No match | Author verified, org key not in domain's NIP-05 |
| Valid | None | Match | N/A | Individual verified, no org attestation |
| Valid | Valid | No match | Match | Org vouches for author, but author not in NIP-05 |
| Invalid | Any | Any | Any | Message may be tampered or forged |
| None | None | N/A | N/A | Standard email, no Nostr verification |

### Interaction with Encryption

Nostr signing is orthogonal to encryption. The same headers work with both plaintext and encrypted messages:

- **Signed only**: Anyone can read; signature proves authorship
- **Signed + encrypted**: Content protected via NIP-44; signature proves authorship
- **Encrypted only**: Not recommended (cannot verify sender)

When used with NIP-44 encryption, the signature covers the ciphertext, not the plaintext. Additional headers indicate encryption:

```
X-Nostr-Signature: p=<author_pubkey>;s=<sig>;h=date;from;message-id;to;r=author
X-Nostr-Encrypted: true
X-Nostr-Recipient: <recipient pubkey>
X-Nostr-Algorithm: nip44
```

## Security Considerations

### Replay Attacks

The signature includes `to`, `date`, and `message-id` headers, preventing:
- Forwarding a signed message to different recipients
- Replaying old messages (date validation)
- Duplicate message injection (unique message-id)

Verifiers SHOULD reject messages with dates significantly in the past or future.

### Counter-Signature Security

Organization counter-signatures provide several guarantees:
- The author signature was valid at signing time
- The organization reviewed and approved the message
- The author is authorized to send on behalf of the organization

However:
- A compromised organization key can vouch for any author signature
- Organizations SHOULD use separate keys for email attestation vs other purposes
- Key rotation requires updating NIP-05 entries

### Key Rotation

If a sender rotates their Nostr key:
- Old emails remain verifiable if the pubkey is cached
- NIP-05 lookup returns the new key, causing mismatch for old emails
- Implementations SHOULD store verification results at receive time rather than re-verifying later

### NIP-05 Availability

If the sender's domain is unavailable during verification:
- Signature verification can still succeed
- NIP-05 cross-verification fails gracefully
- Implementations MAY cache NIP-05 results with appropriate TTL
- Trust-on-first-use: remember pubkey -> email mapping

### Mailing List Forwarding

When mailing lists modify or wrap messages:
- Original signatures may become invalid due to header/body changes
- Lists SHOULD preserve original signatures and add their own with `r=gateway`
- Lists MAY wrap the original message as a MIME attachment

This is analogous to DKIM breakage on forwarded mail.

### Header Injection

Malicious senders could attempt to include crafted header values. The canonicalization process normalizes whitespace and line endings to prevent injection attacks. The structured `key=value` format also limits injection vectors.

### Pubkey Binding

The signature binds the pubkey to the message content. An attacker cannot substitute a different pubkey without invalidating the signature. NIP-05 cross-verification provides additional assurance that the pubkey belongs to the claimed sender domain.

### Multiple Signature Ordering

When processing multiple X-Nostr-Signature headers:
- Signatures are processed in order of appearance
- Counter-signatures (`r=organization`, `r=gateway`) that include `x-nostr-signature` in `h=` cover preceding signatures
- Implementations SHOULD verify the author signature before trusting counter-signatures

## Implementation

### Reference Implementation

- **Go**: [cloistr-email](https://git.coldforge.xyz/coldforge/cloistr-email) - Full signing and verification
  - `internal/email/signing.go` - Email signing
  - `internal/email/verify.go` - Signature verification
  - `internal/signing/signer.go` - BIP-340 Schnorr via go-nostr

### Libraries

- **go-nostr**: BIP-340 Schnorr signatures for Go
- **nostr-tools**: JavaScript implementation for browsers

## Test Vectors

Test vectors ensure compatible implementations across languages.

### Canonicalization Test

**Input Headers:**
```
From: Alice <alice@example.com>
To: bob@example.com
Subject: Test Message
Date: Mon, 01 Jan 2024 12:00:00 +0000
Message-ID: <test123@example.com>
```

**Input Body:**
```
Hello Bob,

This is a test message.
With trailing spaces.

```

**Canonical Headers (sorted):**
```
date:Mon, 01 Jan 2024 12:00:00 +0000
from:Alice <alice@example.com>
message-id:<test123@example.com>
subject:Test Message
to:bob@example.com
```

**Canonical Body:**
```
Hello Bob,

This is a test message.
With trailing spaces.
```
(Note: trailing spaces removed, trailing blank line removed)

### Single Signature Test

**Private Key (hex):**
```
0000000000000000000000000000000000000000000000000000000000000001
```

**Public Key (hex):**
```
79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798
```

**Signed Headers:**
```
date;from;message-id;subject;to
```

**Expected Header:**
```
X-Nostr-Signature: p=79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798;s=<computed_sig>;h=date;from;message-id;subject;to;r=author
```

### Counter-Signature Test

Given the author signature above, an organization counter-signature would:

**Organization Private Key (hex):**
```
0000000000000000000000000000000000000000000000000000000000000002
```

**Organization Public Key (hex):**
```
c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5
```

**Counter-signed Headers:**
```
date;from;x-nostr-signature
```

**Expected Headers (both):**
```
X-Nostr-Signature: p=79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798;s=<author_sig>;h=date;from;message-id;subject;to;r=author
X-Nostr-Signature: p=c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5;s=<org_sig>;h=date;from;x-nostr-signature;r=organization
```

## Compatibility

### With Existing Email

Nostr signature headers are standard RFC 5322 headers. Email clients that don't understand them simply ignore them. The signature is invisible overhead until someone checks it.

### With DKIM/SPF/DMARC

These systems can coexist. A message can have both DKIM and Nostr signatures. Receivers can check either or both. Over time, as Nostr verification spreads, DNS-based systems become supplementary.

The key difference: DKIM authenticates the sending server; Nostr authenticates the author. Both can be valuable:
- DKIM proves the message passed through authorized infrastructure
- Nostr proves the claimed author actually wrote it

### With Nostr Native Messaging

NIP-17 (Gift Wrap) provides Nostr-native private messaging. This NIP focuses on SMTP interoperability. The same keypair can be used for both, providing unified identity across protocols.

## References

- [BIP-340](https://github.com/bitcoin/bips/blob/master/bip-0340.mediawiki): Schnorr Signatures for secp256k1
- [NIP-05](https://github.com/nostr-protocol/nips/blob/master/05.md): Mapping Nostr keys to DNS-based identifiers
- [NIP-07](https://github.com/nostr-protocol/nips/blob/master/07.md): Browser extension signing
- [NIP-44](https://github.com/nostr-protocol/nips/blob/master/44.md): Versioned Encryption
- [NIP-46](https://github.com/nostr-protocol/nips/blob/master/46.md): Nostr Remote Signing
- [RFC 5322](https://tools.ietf.org/html/rfc5322): Internet Message Format
- [RFC 6376](https://tools.ietf.org/html/rfc6376): DomainKeys Identified Mail (DKIM)
