/**
 * email-compose.ts
 *
 * Pure utility functions for composing reply and forward emails.
 * Extracted from ComposePage to enable unit testing without a DOM.
 */
import { ENCRYPTED_SUBJECT_PLACEHOLDER, wrapProtectedHeaders } from './protected-headers'
import { encryptBody } from './body-crypto'
import { encryptAttachment, withUploadAuth, type EnvelopeSigner } from './attachment-crypto'
import type { ClientModeSigner } from './client-signer'
import { pooledSigner, DEFAULT_SIGNER_CONCURRENCY } from './signer-pool'
import type { AttachmentRequest, SendEmailRequest } from './api'

/**
 * Builds a quoted body block for a reply.
 * Prefixes every line of the original body with "> ".
 */
export function quoteBody(from: string, date: string, body: string): string {
  const lines = (body || '').split('\n').map((l) => `> ${l}`)
  return `\n\n---\nOn ${date}, ${from} wrote:\n${lines.join('\n')}`
}

/**
 * Builds a forwarded-message block.
 */
export function forwardBlock(
  from: string,
  date: string,
  subject: string,
  body: string,
): string {
  return `\n\n---\n---------- Forwarded message ----------\nFrom: ${from}\nDate: ${date}\nSubject: ${subject}\n\n${body || ''}`
}

/**
 * Computes the Re: subject for a reply, avoiding double "Re: Re:".
 */
export function replySubject(original: string): string {
  return original.startsWith('Re:') ? original : `Re: ${original}`
}

/**
 * Computes the Fwd: subject for a forward, avoiding double "Fwd: Fwd:".
 */
export function forwardSubject(original: string): string {
  return original.startsWith('Fwd:') ? original : `Fwd: ${original}`
}

/**
 * The parts of a client-side (end-to-end) send that carry content: the body
 * encrypted to the recipient with the real subject sealed inside it as a
 * protected header, and the placeholder as the only subject in the clear.
 */
export async function buildClientEncryptedSend(
  msg: { subject: string; body: string },
  recipientPubkeys: string[],
  signer: EnvelopeSigner,
): Promise<{ subject: string; body: undefined; pre_encrypted_body: string }> {
  return {
    subject: ENCRYPTED_SUBJECT_PLACEHOLDER,
    body: undefined,
    pre_encrypted_body: await encryptBody(wrapProtectedHeaders(msg.subject, msg.body), recipientPubkeys, signer),
  }
}

/**
 * Everything a client-side (end-to-end) send carries, built through one
 * signer (the session's: NIP-46 or NIP-07). The body, with the real subject
 * inside, and each attachment are encrypted under their own keys wrapped to
 * the recipient and the sender; each attachment also carries the sender's own
 * signed Blossom upload authorization.
 */
export async function buildClientModeSend(
  msg: { subject: string; body: string; attachments: AttachmentRequest[] },
  recipients: Record<string, string>,
  sessionSigner: ClientModeSigner,
  opts: { signerConcurrency?: number } = {},
): Promise<Pick<SendEmailRequest, 'subject' | 'body' | 'pre_encrypted_body' | 'recipient_pubkeys' | 'attachments'>> {
  const pubkeys = [...new Set(Object.values(recipients))]
  // Concurrent, but never more than a few calls at the shared signer at once.
  const signer = pooledSigner(sessionSigner, opts.signerConcurrency ?? DEFAULT_SIGNER_CONCURRENCY)
  // The body and every attachment are independent: their key wraps all go to
  // the signer at once. Each attachment's upload authorization has to follow
  // that attachment's encryption (it names the ciphertext's hash), so the
  // critical path is one wrap round plus one signature, whatever R and A are.
  const [content, attachments] = await Promise.all([
    buildClientEncryptedSend(msg, pubkeys, signer),
    Promise.all(
      msg.attachments.map(async (att) =>
        withUploadAuth(await encryptAttachment(att, pubkeys, signer), (ev) => signer.signEvent(ev)),
      ),
    ),
  ])
  return {
    ...content,
    recipient_pubkeys: recipients,
    ...(attachments.length > 0 ? { attachments } : {}),
  }
}

/**
 * A client-side send that cannot be encrypted for everyone it is addressed to.
 * Every recipient gets the same message, so one without a key would receive
 * ciphertext they cannot open. The send stops and names them instead.
 */
export class RecipientKeyError extends Error {
  readonly missing: string[]
  constructor(missing: string[]) {
    super(
      `Cannot encrypt for ${missing.join(', ')}: no Nostr key found. ` +
        'Remove them, or send without end-to-end encryption.',
    )
    this.name = 'RecipientKeyError'
    this.missing = missing
  }
}

/**
 * Every recipient's pubkey for a client-side send (To and every CC), using
 * keys already known and discovering the rest in parallel. Throws
 * RecipientKeyError naming everyone without a key, before anything is
 * encrypted or sent.
 */
export async function resolveClientRecipients(
  addresses: string[],
  known: Record<string, string>,
  discover: (email: string) => Promise<string | null>,
): Promise<Record<string, string>> {
  const unique = [...new Set(addresses.map((a) => a.trim()).filter(Boolean))]
  const resolved = await Promise.all(
    unique.map(async (addr) => {
      if (known[addr]) return [addr, known[addr]] as const
      try {
        return [addr, await discover(addr)] as const
      } catch {
        return [addr, null] as const
      }
    }),
  )
  const missing = resolved.filter(([, pk]) => !pk).map(([addr]) => addr)
  if (missing.length > 0) throw new RecipientKeyError(missing)
  return Object.fromEntries(resolved) as Record<string, string>
}

/**
 * Server answers that mean "this could not be sent encrypted, and nothing was
 * sent". Only these may offer the user a plaintext send instead.
 */
export const ENCRYPTION_REFUSAL_CODES = new Set([
  'SERVER_ENCRYPTION_FAILED',
  'SERVER_ENCRYPTION_ONE_RECIPIENT',
  'SERVER_ENCRYPTION_NO_ATTACHMENTS',
  'RECIPIENT_NOT_ENCRYPTED',
  'CLIENT_MODE_BCC_UNSUPPORTED',
])

/** Whether a failed send was refused for an encryption reason (and so sent nothing). */
export function isEncryptionRefusal(err: unknown): boolean {
  if (err instanceof RecipientKeyError) return true
  const code = (err as { response?: { data?: { code?: unknown } } } | null)?.response?.data?.code
  return typeof code === 'string' && ENCRYPTION_REFUSAL_CODES.has(code)
}
