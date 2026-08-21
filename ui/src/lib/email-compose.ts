/**
 * email-compose.ts
 *
 * Pure utility functions for composing reply and forward emails.
 * Extracted from ComposePage to enable unit testing without a DOM.
 */

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
