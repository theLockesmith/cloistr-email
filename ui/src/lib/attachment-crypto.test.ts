import { describe, it, expect } from 'vitest'
import { nip44, generateSecretKey, getPublicKey } from 'nostr-tools'
import {
  encryptAttachmentsForRecipient,
  decryptAttachmentCiphertext,
  MAX_CLIENT_ENCRYPTED_ATTACHMENT_BYTES,
} from './attachment-crypto'
import type { AttachmentRequest } from './api'

// Real NIP-44 between two keys, standing in for the browser extension.
const senderSk = generateSecretKey()
const recipientSk = generateSecretKey()
const recipientPk = getPublicKey(recipientSk)
const senderPk = getPublicKey(senderSk)
const encryptAsSender = async (pk: string, pt: string) =>
  nip44.encrypt(pt, nip44.getConversationKey(senderSk, pk))
const decryptAsRecipient = async (pk: string, ct: string) =>
  nip44.decrypt(ct, nip44.getConversationKey(recipientSk, pk))

const fileB64 = btoa('%PDF-1.7 a confidential attachment')
const att: AttachmentRequest = { filename: 'secret.pdf', content_type: 'application/pdf', data_base64: fileB64 }

describe('encryptAttachmentsForRecipient', () => {
  it('never sends the file bytes in the clear', async () => {
    const [out] = await encryptAttachmentsForRecipient([att], recipientPk, encryptAsSender)
    expect(out.data_base64).not.toBe(fileB64)
    expect(atob(out.data_base64)).not.toContain('confidential')
    expect(out.filename).toBe('secret.pdf')
  })

  it('round-trips to the recipient', async () => {
    const [out] = await encryptAttachmentsForRecipient([att], recipientPk, encryptAsSender)
    const back = await decryptAttachmentCiphertext(atob(out.data_base64), senderPk, decryptAsRecipient)
    expect(back).toBe(fileB64)
  })

  it('refuses a file too large for one NIP-44 message instead of sending it unencrypted', async () => {
    const big = 'A'.repeat(Math.ceil((MAX_CLIENT_ENCRYPTED_ATTACHMENT_BYTES + 1) / 3) * 4)
    await expect(
      encryptAttachmentsForRecipient([{ filename: 'big.bin', data_base64: big }], recipientPk, encryptAsSender),
    ).rejects.toThrow(/big\.bin/)
  })
})
