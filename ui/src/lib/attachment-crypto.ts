/**
 * attachment-crypto.ts
 *
 * Client-side (end-to-end) encryption of attachments. Client mode promises the
 * server never sees attachment contents; each file is encrypted on the device
 * before it is sent.
 *
 * HYBRID, NOT A NEW FORMAT. Each attachment gets its own random content key.
 * The file and its name/type are encrypted under that key with the shared
 * blob cipher (@cloistr/collab-common/storage, XChaCha20-Poly1305), and the key
 * is wrapped to each recipient AND to the sender with the shared envelope
 * primitive (@cloistr/auth envelope, NIP-44 v2). See
 * architecture/key-wrapping-and-collection-keys.md: wrap keys, never derive,
 * and every key is also wrapped to its owner, so the sender can open their
 * own sent copy. This replaces sending the file through NIP-44 directly, which
 * capped attachments at ~48 KB.
 *
 * What travels: the attachment's bytes are the JSON of EncryptedAttachmentV1,
 * with an opaque filename and content type, so the server sees neither the
 * contents nor the real name or type. The server refuses any client-mode
 * attachment that is not this shape.
 */
import { generateContentKey, wrapKeyForRecipient, unwrapKeyFromSender, type SignerInterface } from '@cloistr/auth'
import { encryptBlob, decryptBlob } from '@cloistr/collab-common/storage'
import type { AttachmentRequest, SignedNostrEvent } from './api'

/** Content type the server sees for every client-encrypted attachment. */
export const ENCRYPTED_ATTACHMENT_TYPE = 'application/vnd.cloistr.encrypted-attachment+json'
/** Filename the server sees; the real one is inside `meta`. */
export const ENCRYPTED_ATTACHMENT_FILENAME = 'encrypted-attachment'

type EncryptedBlob = Awaited<ReturnType<typeof encryptBlob>>

export interface EncryptedAttachmentV1 {
  v: 1
  /** Random id. The envelope subject, so an envelope moved between attachments is refused. */
  id: string
  /** Hex pubkey every envelope's ECDH is against: the sender's. */
  sender: string
  /** Hex pubkey -> the content key wrapped to that person. Recipients and the sender. */
  keys: Record<string, string>
  /** JSON {filename, content_type}, encrypted under the content key. */
  meta: EncryptedBlob
  /** The file bytes, encrypted under the content key. */
  data: EncryptedBlob
}

/** The parts of a signer this needs: NIP-44 only, never NIP-04. */
export type EnvelopeSigner = Pick<SignerInterface, 'getPublicKey' | 'nip44Encrypt' | 'nip44Decrypt'>

export interface DecryptedAttachment {
  filename: string
  content_type?: string
  data_base64: string
}

function bytesToBase64(bytes: Uint8Array): string {
  let s = ''
  const CHUNK = 0x8000
  for (let i = 0; i < bytes.length; i += CHUNK) {
    s += String.fromCharCode(...bytes.subarray(i, i + CHUNK))
  }
  return btoa(s)
}

function base64ToBytes(b64: string): Uint8Array<ArrayBuffer> {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

const utf8 = new TextEncoder()
const fromUtf8 = new TextDecoder()

/**
 * Wraps a content key to every recipient and to the sender. The sender is
 * always a recipient of their own key, so they can open their own sent copy;
 * deduped in case they also sent to themselves.
 */
export async function wrapContentKey(
  key: Uint8Array,
  subject: string,
  recipientPubkeys: string[],
  sender: string,
  signer: EnvelopeSigner,
): Promise<Record<string, string>> {
  const keys: Record<string, string> = {}
  for (const pk of new Set([...recipientPubkeys, sender])) {
    keys[pk] = await wrapKeyForRecipient(key, subject, pk, signer as SignerInterface)
  }
  return keys
}

export async function encryptAttachment(
  att: AttachmentRequest,
  recipientPubkeys: string[],
  signer: EnvelopeSigner,
): Promise<AttachmentRequest> {
  const sender = await signer.getPublicKey()
  const key = generateContentKey()
  const id = crypto.randomUUID()

  const data = await encryptBlob(base64ToBytes(att.data_base64), key)
  const meta = await encryptBlob(utf8.encode(JSON.stringify({ filename: att.filename, content_type: att.content_type })), key)

  const keys = await wrapContentKey(key, id, recipientPubkeys, sender, signer)

  const envelope: EncryptedAttachmentV1 = { v: 1, id, sender, keys, meta, data }
  return {
    filename: ENCRYPTED_ATTACHMENT_FILENAME,
    content_type: ENCRYPTED_ATTACHMENT_TYPE,
    data_base64: bytesToBase64(utf8.encode(JSON.stringify(envelope))),
  }
}

export async function encryptAttachmentsForRecipients(
  attachments: AttachmentRequest[],
  recipientPubkeys: string[],
  signer: EnvelopeSigner,
): Promise<AttachmentRequest[]> {
  const out: AttachmentRequest[] = []
  for (const att of attachments) {
    out.push(await encryptAttachment(att, recipientPubkeys, signer))
  }
  return out
}

/**
 * Opens a client-encrypted attachment, given the bytes the server returned as
 * text. Throws if this viewer holds no key for it or anything fails to verify.
 */
export async function decryptAttachment(serialized: string, signer: EnvelopeSigner): Promise<DecryptedAttachment> {
  const env = JSON.parse(serialized) as EncryptedAttachmentV1
  if (env.v !== 1) throw new Error(`Unsupported encrypted attachment version: ${String(env.v)}`)
  const me = await signer.getPublicKey()
  const wrapped = env.keys[me]
  if (!wrapped) throw new Error('This attachment was not encrypted to your key')

  const key = await unwrapKeyFromSender(wrapped, env.id, env.sender, signer as SignerInterface)
  const meta = JSON.parse(fromUtf8.decode(await decryptBlob(env.meta, key))) as { filename: string; content_type?: string }
  const data = await decryptBlob(env.data, key)
  return { filename: meta.filename, content_type: meta.content_type, data_base64: bytesToBase64(data) }
}

/** How long a client-signed upload authorization stays valid. */
const UPLOAD_AUTH_TTL_SECONDS = 15 * 60

type UnsignedEvent = { kind: number; created_at: number; tags: string[][]; content: string }

async function sha256Hex(bytes: Uint8Array<ArrayBuffer>): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', bytes)
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('')
}

/**
 * Signs, with the sender's own key, the Blossom upload authorization (BUD-01
 * kind 24242) for exactly this attachment's bytes. Client-side signing: the
 * server stores client-mode attachments only under the sender's authorization
 * and never signs for them. The authorization names the blob's hash, so it
 * cannot be used to upload anything else.
 */
export async function withUploadAuth(
  att: AttachmentRequest,
  sign: (event: UnsignedEvent) => Promise<SignedNostrEvent>,
  nowSeconds = Math.floor(Date.now() / 1000),
): Promise<AttachmentRequest> {
  const hash = await sha256Hex(base64ToBytes(att.data_base64))
  const upload_auth = await sign({
    kind: 24242,
    created_at: nowSeconds,
    tags: [
      ['t', 'upload'],
      ['x', hash],
      ['expiration', String(nowSeconds + UPLOAD_AUTH_TTL_SECONDS)],
    ],
    content: 'upload blob',
  })
  return { ...att, upload_auth }
}
