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
  msg: { to: string; subject: string; body: string; attachments: AttachmentRequest[] },
  recipientPubkey: string,
  signer: ClientModeSigner,
): Promise<Pick<SendEmailRequest, 'subject' | 'body' | 'pre_encrypted_body' | 'recipient_pubkeys' | 'attachments'>> {
  const content = await buildClientEncryptedSend(msg, [recipientPubkey], signer)
  const attachments: AttachmentRequest[] = []
  for (const att of msg.attachments) {
    const sealed = await encryptAttachment(att, [recipientPubkey], signer)
    attachments.push(await withUploadAuth(sealed, (ev) => signer.signEvent(ev)))
  }
  return {
    ...content,
    recipient_pubkeys: { [msg.to]: recipientPubkey },
    ...(attachments.length > 0 ? { attachments } : {}),
  }
}
