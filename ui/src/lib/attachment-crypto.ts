/**
 * attachment-crypto.ts
 *
 * Client-side (end-to-end) encryption of attachments, using the same NIP-44
 * scheme as the message body. Client mode promises the server never sees
 * plaintext; before this, only the body was encrypted and attachments went to
 * the server, the outbound mail and Blossom as readable files.
 *
 * The encrypted text is the NIP-44 payload of the file's base64. It travels in
 * the request's data_base64 field base64-encoded once more, because that field
 * is decoded to bytes on the server. The server refuses any client-mode
 * attachment that is not a NIP-44 payload.
 */
import type { AttachmentRequest } from './api'

/** NIP-44 encrypts at most 65535 bytes of text in one message. */
const NIP44_MAX_PLAINTEXT = 65535

/**
 * Largest file that fits one NIP-44 message once base64-encoded (4 chars per
 * 3 bytes). Bigger files need a chunked or hybrid scheme, which is not built
 * yet, so they are refused rather than sent unencrypted.
 */
export const MAX_CLIENT_ENCRYPTED_ATTACHMENT_BYTES = Math.floor(NIP44_MAX_PLAINTEXT / 4) * 3

type Encrypt = (pubkey: string, plaintext: string) => Promise<string>
type Decrypt = (pubkey: string, ciphertext: string) => Promise<string>

export async function encryptAttachmentsForRecipient(
  attachments: AttachmentRequest[],
  recipientPubkey: string,
  encrypt: Encrypt,
): Promise<AttachmentRequest[]> {
  const out: AttachmentRequest[] = []
  for (const att of attachments) {
    if (att.data_base64.length > NIP44_MAX_PLAINTEXT) {
      const kb = Math.floor(MAX_CLIENT_ENCRYPTED_ATTACHMENT_BYTES / 1024)
      throw new Error(`${att.filename} is too large to encrypt (encrypted attachments are limited to ${kb} KB for now)`)
    }
    const ciphertext = await encrypt(recipientPubkey, att.data_base64)
    out.push({ ...att, data_base64: btoa(ciphertext) })
  }
  return out
}

/** Decrypts a client-mode attachment to the file's base64. */
export function decryptAttachmentCiphertext(ciphertext: string, peerPubkey: string, decrypt: Decrypt): Promise<string> {
  return decrypt(peerPubkey, ciphertext)
}
