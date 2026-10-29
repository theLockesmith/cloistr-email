/**
 * server-draft.ts
 *
 * Builds and opens server-side drafts (POST/PUT /api/v2/email/drafts).
 *
 * An unencrypted draft is stored as written, like an unencrypted sent copy.
 * An encrypted draft (client or server mode) is sealed here to the user's own
 * key with the body envelope, the real subject inside as a protected header,
 * so the server never holds its plaintext. With no signer able to do that the
 * draft is not sent to the server at all; the local autosave still keeps it.
 */

import type { EnvelopeSigner } from './attachment-crypto'
import { encryptBody, openClientBody } from './body-crypto'
import { ENCRYPTED_SUBJECT_PLACEHOLDER, parseProtectedHeaders, wrapProtectedHeaders } from './protected-headers'
import type { Email, SaveDraftRequest } from './api'
import type { EncryptionMode } from './nostr'

/** How long typing must pause before the draft is saved to the server. */
export const SERVER_DRAFT_DELAY_MS = 3000

export interface ComposeFields {
  to: string
  cc: string
  subject: string
  body: string
  encryptionMode: EncryptionMode
}

export type ServerDraftRequest = SaveDraftRequest

function splitList(value: string): string[] {
  return value
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}

function joinList(value: string | string[] | undefined): string {
  if (!value) return ''
  return Array.isArray(value) ? value.join(', ') : value
}

/**
 * Returns the request body for saving `fields` as a server draft, or null when
 * the draft is encrypted and no signer can seal it.
 */
export async function buildServerDraft(
  fields: ComposeFields,
  signer: EnvelopeSigner | null,
): Promise<ServerDraftRequest | null> {
  const cc = splitList(fields.cc)
  const base = { to: fields.to.trim(), ...(cc.length > 0 ? { cc } : {}) }
  if (fields.encryptionMode === 'none') {
    return { ...base, subject: fields.subject, body: fields.body, encryption_mode: 'none' }
  }
  if (!signer) return null
  // No recipients: the content key is wrapped to the sender alone.
  const sealed = await encryptBody(wrapProtectedHeaders(fields.subject, fields.body), [], signer)
  return { ...base, subject: ENCRYPTED_SUBJECT_PLACEHOLDER, body: sealed, encryption_mode: 'client' }
}

/** Reopens a stored draft into compose fields. Throws if it cannot be opened. */
export async function openServerDraft(email: Email, signer: EnvelopeSigner | null): Promise<ComposeFields> {
  const to = joinList(email.to)
  const cc = joinList(email.cc)
  if (!email.requires_client_decryption && !email.encrypted_body) {
    return { to, cc, subject: email.subject, body: email.body ?? '', encryptionMode: 'none' }
  }
  if (!signer?.nip44Decrypt || !email.encrypted_body) {
    throw new Error('This draft is encrypted and your signer cannot open it')
  }
  const legacyDecrypt = signer.nip44Decrypt.bind(signer)
  const plaintext = await openClientBody(email.encrypted_body, email.sender_pubkey ?? '', signer, legacyDecrypt)
  const opened = parseProtectedHeaders(plaintext)
  return { to, cc, subject: opened.subject ?? '', body: opened.body, encryptionMode: 'client' }
}
