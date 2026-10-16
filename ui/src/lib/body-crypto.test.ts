import { describe, it, expect } from 'vitest'
import { nip44, generateSecretKey, getPublicKey } from 'nostr-tools'
import { encryptBody, openClientBody } from './body-crypto'
import type { EnvelopeSigner } from './attachment-crypto'

function realSigner(): EnvelopeSigner & { sk: Uint8Array } {
  const sk = generateSecretKey()
  const pk = getPublicKey(sk)
  return {
    sk,
    getPublicKey: async () => pk,
    nip44Encrypt: async (peer: string, pt: string) => nip44.encrypt(pt, nip44.getConversationKey(sk, peer)),
    nip44Decrypt: async (peer: string, ct: string) => nip44.decrypt(ct, nip44.getConversationKey(sk, peer)),
  }
}
const legacyFor = (s: EnvelopeSigner) => (peer: string, ct: string) => s.nip44Decrypt!(peer, ct)

describe('body envelope', () => {
  const BODY = 'Subject line inside\n\nthe confidential numbers'

  it('carries no plaintext and opens for the recipient', async () => {
    const sender = realSigner()
    const recipient = realSigner()
    const enc = await encryptBody(BODY, [await recipient.getPublicKey()], sender)
    expect(enc).not.toContain('confidential')
    expect(await openClientBody(enc, await sender.getPublicKey(), recipient, legacyFor(recipient))).toBe(BODY)
  })

  // Before this, the body was encrypted to the recipient only and the sender's
  // own sent copy was unreadable to them.
  it('opens for the sender, from their own sent copy', async () => {
    const sender = realSigner()
    const recipient = realSigner()
    const enc = await encryptBody(BODY, [await recipient.getPublicKey()], sender)
    expect(await openClientBody(enc, await sender.getPublicKey(), sender, legacyFor(sender))).toBe(BODY)
  })

  it('does not open for anyone else', async () => {
    const sender = realSigner()
    const enc = await encryptBody(BODY, [await realSigner().getPublicKey()], sender)
    const stranger = realSigner()
    await expect(openClientBody(enc, await sender.getPublicKey(), stranger, legacyFor(stranger))).rejects.toThrow(
      /not encrypted to your key/,
    )
  })

  it('has no 64 KB limit', async () => {
    const sender = realSigner()
    const recipient = realSigner()
    const big = 'x'.repeat(300_000)
    const enc = await encryptBody(big, [await recipient.getPublicKey()], sender)
    expect(await openClientBody(enc, await sender.getPublicKey(), recipient, legacyFor(recipient))).toBe(big)
  })

  // Backward compatibility: messages in the older format still open.
  it('still opens an older NIP-44-to-recipient body', async () => {
    const sender = realSigner()
    const recipient = realSigner()
    const legacy = nip44.encrypt(BODY, nip44.getConversationKey(sender.sk, await recipient.getPublicKey()))
    expect(await openClientBody(legacy, await sender.getPublicKey(), recipient, legacyFor(recipient))).toBe(BODY)
  })
})
