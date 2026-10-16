/**
 * body-crypto.ts
 *
 * Client-side (end-to-end) encryption of the message body, on the same
 * envelope model as attachments (attachment-crypto.ts): a random content key
 * encrypts the body with the shared blob cipher, and the key is wrapped to
 * each recipient AND to the sender with the shared envelope primitive. The
 * body already carries the real subject as a protected header.
 *
 * Before this the body was NIP-44 encrypted to the first recipient only, so
 * the sender could not read their own sent copy, and one NIP-44 message caps
 * text at 65535 bytes. That older format still opens (openClientBody), so
 * messages sent before this change stay readable.
 */
import { generateContentKey, unwrapKeyFromSender, type SignerInterface } from '@cloistr/auth'
import { encryptBlob, decryptBlob } from '@cloistr/collab-common/storage'
import { wrapContentKey, type EnvelopeSigner } from './attachment-crypto'

type EncryptedBlob = Awaited<ReturnType<typeof encryptBlob>>

export interface EncryptedBodyV1 {
  v: 1
  /** Distinguishes a body envelope from an attachment envelope. */
  type: 'body'
  /** Random id: the envelope subject. */
  id: string
  /** Hex pubkey every envelope's ECDH is against: the sender's. */
  sender: string
  /** Hex pubkey -> the content key wrapped to that person (recipients and sender). */
  keys: Record<string, string>
  /** The body text (with its protected headers), encrypted under the content key. */
  data: EncryptedBlob
}

const utf8 = new TextEncoder()
const fromUtf8 = new TextDecoder()

export async function encryptBody(plaintext: string, recipientPubkeys: string[], signer: EnvelopeSigner): Promise<string> {
  const sender = await signer.getPublicKey()
  const key = generateContentKey()
  const id = crypto.randomUUID()
  const data = await encryptBlob(utf8.encode(plaintext), key)
  const keys = await wrapContentKey(key, id, recipientPubkeys, sender, signer)
  const env: EncryptedBodyV1 = { v: 1, type: 'body', id, sender, keys, data }
  return JSON.stringify(env)
}

function parseBodyEnvelope(text: string): EncryptedBodyV1 | null {
  if (!text.startsWith('{')) return null
  try {
    const env = JSON.parse(text) as EncryptedBodyV1
    return env && env.v === 1 && env.type === 'body' ? env : null
  } catch {
    return null
  }
}

/**
 * Opens a client-encrypted body in either format: the envelope, or the older
 * NIP-44-to-recipient payload (decrypted against the sender's pubkey through
 * `legacyDecrypt`, as before).
 */
export async function openClientBody(
  encryptedBody: string,
  senderPubkey: string,
  signer: EnvelopeSigner,
  legacyDecrypt: (peer: string, ciphertext: string) => Promise<string>,
): Promise<string> {
  const env = parseBodyEnvelope(encryptedBody)
  if (!env) return legacyDecrypt(senderPubkey, encryptedBody)
  const me = await signer.getPublicKey()
  const wrapped = env.keys[me]
  if (!wrapped) throw new Error('This message was not encrypted to your key')
  const key = await unwrapKeyFromSender(wrapped, env.id, env.sender, signer as SignerInterface)
  return fromUtf8.decode(await decryptBlob(env.data, key))
}
