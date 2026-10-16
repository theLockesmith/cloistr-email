/**
 * protected-headers.ts
 *
 * Hides the subject of a client-side (end-to-end) message. The real subject is
 * written inside the encrypted body as a "protected header" block, the same
 * practice OpenPGP and S/MIME mail uses, and the subject outside the ciphertext
 * is a fixed placeholder (the server enforces that too). SMTP is untouched:
 * the outer message is still an ordinary email, and a client that knows
 * nothing of this shows the header block as the first lines of the body,
 * which is still readable.
 *
 * Block format (the marker line is what makes parsing safe: a body that merely
 * starts with "Subject:" is left alone):
 *
 *   Content-Type: text/plain; charset="utf-8"; protected-headers="v1"
 *   Subject: <real subject>
 *   <blank line>
 *   <body>
 */

/** The subject every client-encrypted message carries in the clear. */
export const ENCRYPTED_SUBJECT_PLACEHOLDER = 'Encrypted message'

const MARKER = 'Content-Type: text/plain; charset="utf-8"; protected-headers="v1"'

/** Wraps the real subject into the plaintext that will be encrypted. */
export function wrapProtectedHeaders(subject: string, body: string): string {
  // A header value is one line; fold any line breaks a user pasted in.
  const oneLine = subject.replace(/[\r\n]+/g, ' ')
  return `${MARKER}\r\nSubject: ${oneLine}\r\n\r\n${body}`
}

/**
 * Splits decrypted plaintext into the protected subject and the body. Text
 * without the marker (older messages, other clients) comes back unchanged with
 * no subject.
 */
export function parseProtectedHeaders(plaintext: string): { subject?: string; body: string } {
  const normalized = plaintext.replace(/\r\n/g, '\n')
  if (!normalized.startsWith(`${MARKER}\n`)) {
    return { body: plaintext }
  }
  const end = normalized.indexOf('\n\n')
  const headerBlock = end === -1 ? normalized : normalized.slice(0, end)
  const body = end === -1 ? '' : normalized.slice(end + 2)
  let subject: string | undefined
  for (const line of headerBlock.split('\n').slice(1)) {
    const m = /^Subject: ?(.*)$/i.exec(line)
    if (m) subject = m[1]
  }
  return { subject, body }
}

const CACHE_PREFIX = 'email_protected_subject:'

/**
 * Decrypted subjects for the inbox list, kept for this browser tab only
 * (sessionStorage), so a decrypted subject is not left on disk after the tab
 * closes. Storage can be unavailable; failures just mean "not cached".
 */
export function cacheProtectedSubject(emailId: string, subject: string): void {
  try {
    sessionStorage.setItem(CACHE_PREFIX + emailId, subject)
  } catch {
    // not cached
  }
}

export function cachedProtectedSubject(emailId: string): string | undefined {
  try {
    return sessionStorage.getItem(CACHE_PREFIX + emailId) ?? undefined
  } catch {
    return undefined
  }
}
