/**
 * email-compose.ts
 *
 * Pure utility functions for composing reply and forward emails.
 * Extracted from ComposePage to enable unit testing without a DOM.
 */
import { ENCRYPTED_SUBJECT_PLACEHOLDER, wrapProtectedHeaders } from './protected-headers'

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
  recipientPubkey: string,
  encrypt: (pubkey: string, plaintext: string) => Promise<string>,
): Promise<{ subject: string; body: undefined; pre_encrypted_body: string }> {
  return {
    subject: ENCRYPTED_SUBJECT_PLACEHOLDER,
    body: undefined,
    pre_encrypted_body: await encrypt(recipientPubkey, wrapProtectedHeaders(msg.subject, msg.body)),
  }
}
