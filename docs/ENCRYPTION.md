# Email Encryption Design

Cloistr Email provides end-to-end email encryption using Nostr cryptography (NIP-44).

## Concepts

### Why Nostr Keys for Email?

Traditional email encryption (PGP, S/MIME) requires separate key management. With Cloistr Email, we use your existing Nostr keypair:

- **One identity:** Your Nostr key is your email identity
- **No separate keys:** No need for PGP keys or certificates
- **Already Nostr-native:** If you have an Nostr account, you already have encryption
- **nsecbunker integration:** Decryption happens transparently via NIP-46

### Encryption vs. Signing

**Encryption:** Protects email content from being read by others

**Signing:** Proves the email came from you (see [RFC-002](002-nostr-email-integration.md) for the signing design)

Currently we focus on encryption. Email signing with Nostr keys is planned as the identity layer for SMTP - replacing the need for DKIM/SPF/DMARC.

## How It Works

### Sending an Encrypted Email

1. **Compose** - User writes email to `bob@example.com`

2. **Discover key** - Look up Bob's Nostr public key
   - Check contacts first (if Bob is a contact)
   - Query NIP-05 at bob's domain (if bob@example.com has Nostr identity)
   - Ask user if not found

3. **Encrypt** - Encrypt email body using NIP-44
   ```
   plaintext: "Hello Bob, this is secret"
   recipient_key: bob's_npub
   → encrypted_content: "xyz123encrypted..."
   ```

4. **Add metadata** - Include custom headers
   ```
   X-Nostr-Encrypted: true
   X-Nostr-Sender: npub1alice...
   X-Nostr-Recipient: npub1bob...
   X-Nostr-Nonce: <encryption nonce>
   ```

5. **Send** - Submit to Stalwart, which sends as normal SMTP

6. **Store** - Save encrypted email to database
   - Body is encrypted blob
   - Metadata is plaintext (to, subject, etc.)

### Receiving an Encrypted Email

1. **Receive** - Stalwart receives email normally

2. **Store** - Save to database
   - Detect `X-Nostr-Encrypted` header
   - Flag email as encrypted
   - Store encrypted body as-is

3. **Retrieve** - User requests email via API
   - Check if email is encrypted
   - If encrypted, request decryption via nsecbunker (NIP-46)
   - Return decrypted content

4. **Decrypt** - nsecbunker decrypts using user's private key
   ```
   user_key: alice's_secret_key (stored in nsecbunker)
   encrypted_content: "xyz123encrypted..."
   → plaintext: "Hello Alice, this is secret"
   ```

## Email Encryption Format

### Option A: Custom Headers + Encrypted Body (RECOMMENDED)

**Advantages:**
- Simplest implementation
- Clear encryption metadata
- Easy to extend

**Format:**

```
From: alice@cloistr.xyz
To: bob@example.com
Subject: Hello Bob
X-Nostr-Encrypted: true
X-Nostr-Sender: npub1alice...
X-Nostr-Recipient: npub1bob...
X-Nostr-Nonce: <base64-encoded-nonce>
X-Nostr-Algorithm: nip44

<base64-encoded-nip44-ciphertext>
```

**Example:**

```
From: alice@cloistr.xyz
To: bob@example.com
Subject: Hello Bob
X-Nostr-Encrypted: true
X-Nostr-Sender: npub1zzth0ysdzzzzz...
X-Nostr-Recipient: npub1bob0sdz...
X-Nostr-Nonce: jK8h+aL9mK2b1...
X-Nostr-Algorithm: nip44

jK8h+aL9mK2b1c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8s9t0u1v2w3x4y5z6
```

### Option B: S/MIME-like Format

**Advantages:**
- Compatible with S/MIME tools
- Standard structure
- Client-side decryption plugins possible

**Format:** Similar to S/MIME but with Nostr keys instead of X.509 certs

### Option C: PGP/MIME Format

**Advantages:**
- Compatible with PGP ecosystem
- Existing client plugins (Thunderbird, etc.)

**Disadvantages:**
- More complex
- Requires PGP/MIME-aware clients for transparent decryption

## Non-Encrypted Emails

### To Nostr Users Without Key On File

**Scenario:** Sending to bob@example.com, Bob has Nostr identity but we didn't find his key

**Behavior:**

1. Prompt user: "Bob's Nostr key was not found. Send unencrypted?"
2. User chooses:
   - **Unencrypted:** Send as normal email (user takes responsibility)
   - **Lookup again:** Try NIP-05 lookup again
   - **Import key manually:** User provides Bob's npub
   - **Cancel:** Don't send

### To Non-Nostr Recipients

**Scenario:** Sending to bob@gmail.com, Bob doesn't have Nostr identity

**Behavior:**

1. Can't encrypt (Bob can't decrypt without Nostr key)
2. Prompt user: "This recipient doesn't have a Nostr key. Send unencrypted?"
3. User chooses:
   - **Unencrypted:** Send normally
   - **Cancel:** Don't send

## Replies and Threads

### Reply to Encrypted Email

**Behavior:**
- **If original was encrypted:** Ask user if they want to encrypt reply
- **Use sender's key:** Encrypt reply to original sender's npub (from email metadata)
- **Maintain context:** Thread is kept together in conversation

**Implementation:**
- When retrieving email, if original was encrypted, include sender's npub
- When composing reply, pre-fill "encrypt to" with sender's npub
- User can change encryption target if needed

## Key Discovery

### Method 1: Contacts Database

**Process:**
1. Check if recipient's email is in user's contacts
2. If contact has npub field, use it
3. Mark as "trusted" (user already approved)

**Advantages:**
- Fastest
- No external lookups
- Trusted source

### Method 2: NIP-05 Lookup

**Process:**
1. Extract domain from email (bob@example.com → example.com)
2. Query `.well-known/nostr.json` at that domain
3. Find matching npub for the email address

**Implementation:**
```go
// Pseudocode
npub := lookupNIP05("bob@example.com")
// 1. GET https://example.com/.well-known/nostr.json
// 2. Parse JSON
// 3. Find "bob" entry
// 4. Return npub if found
```

**Caching:**
- Cache NIP-05 lookups for 24 hours (configurable)
- Mark as "discovered" (not verified)
- User can verify by importing into contacts

**Advantages:**
- Works without maintaining contacts
- Discoverable by domain

**Disadvantages:**
- External lookup (privacy concern)
- Domain must be set up correctly
- Can be slow

### Method 3: Manual Entry

**Process:**
1. User can't find key automatically
2. User enters npub manually
3. Email is encrypted with that key

**Advantages:**
- Works for any recipient
- Out-of-band verification possible

## Security Considerations

### Encryption In Transit

- **SMTP TLS:** Use TLS when sending encrypted emails (Stalwart handles this)
- **HTTP TLS:** API always uses HTTPS in production

### Encryption At Rest

- **Database:** Email bodies are stored encrypted
- **Backups:** Database backups contain encrypted data
- **Logs:** Never log email content or keys

### Key Management

- **Private keys:** Never stored in Cloistr Email
- **Decryption:** Always via nsecbunker (NIP-46)
- **Key compromise:** If user's Nostr key is compromised, encryption fails
  - User should rotate key in nsecbunker

### Metadata Leakage

**What's encrypted:**
- Email body
- Subject line (optional future enhancement)
- Attachments (optional)

**What's plaintext:**
- From/To addresses
- Email headers
- Folder/labels
- Timestamps

**Note:** Email metadata is still visible to Stalwart and email providers. This is inherent to email.

### Third-Party Clients

**IMAP clients** (Thunderbird, Apple Mail):
- Can fetch encrypted emails
- Can't decrypt without plugin
- Options:
  1. Build plugin (decrypt via API)
  2. Server decryption (weaker security)
  3. Web UI only (recommended for v1)

## Implementation Roadmap

### Phase 1: Basic Encryption (v1)

- [ ] Encrypt/decrypt using NIP-44
- [ ] Custom headers for metadata
- [ ] NIP-05 key discovery
- [ ] Web UI only (IMAP clients see encrypted blob)
- [ ] No signing (only encryption)

### Phase 2: Enhanced Discovery

- [ ] Contacts-based key management
- [ ] Manual key import/verification
- [ ] Key verification flow (confirm fingerprint, etc.)

### Phase 3: Advanced Features

- [ ] Subject line encryption
- [ ] Attachment encryption
- [ ] Email signing (NIP-46 signing)
- [ ] IMAP client plugins
- [ ] Conversation encryption (keep keys consistent)

### Phase 4: Interoperability

- [ ] PGP/MIME compatibility
- [ ] S/MIME bridge
- [ ] Key recovery/export

## Testing

### Test Cases

1. **Encrypt to new contact:**
   - Compose email to new recipient
   - Trigger key discovery
   - Encrypt
   - Verify encrypted email stored

2. **Decrypt received email:**
   - Receive encrypted email
   - Request decryption
   - Verify plaintext returned
   - Check metadata preserved

3. **Key discovery fallback:**
   - Try to send to recipient without saved key
   - Query NIP-05
   - Confirm key found or prompt user

4. **Non-Nostr recipient:**
   - Send unencrypted to non-Nostr email
   - Verify sent plaintext

5. **Encryption failure:**
   - Simulate nsecbunker unavailable
   - Verify graceful error handling
   - User can still send unencrypted

## References

- [NIP-44: Encryption Standard](https://github.com/nostr-protocol/nips/blob/master/44.md)
- [NIP-05: Email-based identifier](https://github.com/nostr-protocol/nips/blob/master/05.md)
- [NIP-46: Nostr Connect (Remote Signing)](https://github.com/nostr-protocol/nips/blob/master/46.md)
- [RFC 8551: S/MIME (for reference)](https://datatracker.ietf.org/doc/html/rfc8551)
- [RFC 3156: PGP/MIME (for reference)](https://datatracker.ietf.org/doc/html/rfc3156)
