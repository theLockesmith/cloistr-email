import { describe, it, expect } from 'vitest'
import { nip44, generateSecretKey, getPublicKey } from 'nostr-tools'
import {
  encryptAttachmentsForRecipients,
  decryptAttachment,
  ENCRYPTED_ATTACHMENT_FILENAME,
  ENCRYPTED_ATTACHMENT_TYPE,
  type EnvelopeSigner,
  type EncryptedAttachmentV1,
} from './attachment-crypto'
import type { AttachmentRequest } from './api'

// Signers backed by real secp256k1 keys, not mocks: a mock that returned its
// input would pass every round trip while proving nothing was encrypted.
function realSigner(): EnvelopeSigner & { calls: number } {
  const sk = generateSecretKey()
  const pk = getPublicKey(sk)
  const s = {
    calls: 0,
    getPublicKey: async () => pk,
    nip44Encrypt: async (peer: string, pt: string) => {
      s.calls++
      return nip44.encrypt(pt, nip44.getConversationKey(sk, peer))
    },
    nip44Decrypt: async (peer: string, ct: string) => nip44.decrypt(ct, nip44.getConversationKey(sk, peer)),
  }
  return s
}

const SECRET = '%PDF-1.7 a confidential attachment'
const att: AttachmentRequest = { filename: 'q3-salaries.pdf', content_type: 'application/pdf', data_base64: btoa(SECRET) }
const wire = (a: AttachmentRequest) => atob(a.data_base64)

describe('encrypted attachments', () => {
  it('sends neither the contents nor the real name or type', async () => {
    const sender = realSigner()
    const recipient = realSigner()
    const [out] = await encryptAttachmentsForRecipients([att], [await recipient.getPublicKey()], sender)
    const text = wire(out)
    expect(text).not.toContain('confidential')
    expect(text).not.toContain(btoa(SECRET))
    expect(text).not.toContain('q3-salaries')
    expect(text).not.toContain('application/pdf')
    expect(out.filename).toBe(ENCRYPTED_ATTACHMENT_FILENAME)
    expect(out.content_type).toBe(ENCRYPTED_ATTACHMENT_TYPE)
  })

  it('opens for the recipient, with the real name and type', async () => {
    const sender = realSigner()
    const recipient = realSigner()
    const [out] = await encryptAttachmentsForRecipients([att], [await recipient.getPublicKey()], sender)
    const back = await decryptAttachment(wire(out), recipient)
    expect(atob(back.data_base64)).toBe(SECRET)
    expect(back.filename).toBe('q3-salaries.pdf')
    expect(back.content_type).toBe('application/pdf')
  })

  // The owner envelope is the part most likely to be dropped as redundant.
  it('opens for the sender, from their own copy', async () => {
    const sender = realSigner()
    const recipient = realSigner()
    const [out] = await encryptAttachmentsForRecipients([att], [await recipient.getPublicKey()], sender)
    const back = await decryptAttachment(wire(out), sender)
    expect(atob(back.data_base64)).toBe(SECRET)
  })

  it('does not open for anyone else', async () => {
    const sender = realSigner()
    const recipient = realSigner()
    const stranger = realSigner()
    const [out] = await encryptAttachmentsForRecipients([att], [await recipient.getPublicKey()], sender)
    await expect(decryptAttachment(wire(out), stranger)).rejects.toThrow(/not encrypted to your key/)
  })

  it('refuses an envelope moved from another attachment', async () => {
    const sender = realSigner()
    const recipient = realSigner()
    const rpk = await recipient.getPublicKey()
    const [a, b] = await encryptAttachmentsForRecipients([att, { ...att, filename: 'other.pdf' }], [rpk], sender)
    const envA = JSON.parse(wire(a)) as EncryptedAttachmentV1
    const envB = JSON.parse(wire(b)) as EncryptedAttachmentV1
    envB.keys[rpk] = envA.keys[rpk]
    await expect(decryptAttachment(JSON.stringify(envB), recipient)).rejects.toThrow()
  })

  // The ~48 KB NIP-44 stopgap is gone: a large file round-trips.
  it('handles a file far larger than one NIP-44 message', async () => {
    const sender = realSigner()
    const recipient = realSigner()
    const big = new Uint8Array(3 * 1024 * 1024).map((_, i) => i % 251)
    let bin = ''
    for (let i = 0; i < big.length; i += 0x8000) bin += String.fromCharCode(...big.subarray(i, i + 0x8000))
    const [out] = await encryptAttachmentsForRecipients(
      [{ filename: 'big.bin', data_base64: btoa(bin) }],
      [await recipient.getPublicKey()],
      sender,
    )
    const back = await decryptAttachment(wire(out), recipient)
    expect(back.data_base64).toBe(btoa(bin))
  })

  it('wraps the key once per person, sender included', async () => {
    const sender = realSigner()
    const r1 = realSigner()
    const r2 = realSigner()
    const [out] = await encryptAttachmentsForRecipients([att], [await r1.getPublicKey(), await r2.getPublicKey()], sender)
    const env = JSON.parse(wire(out)) as EncryptedAttachmentV1
    expect(Object.keys(env.keys).sort()).toEqual(
      [await r1.getPublicKey(), await r2.getPublicKey(), await sender.getPublicKey()].sort(),
    )
    expect(sender.calls).toBe(3)
  })
})
