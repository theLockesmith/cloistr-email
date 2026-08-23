/**
 * draft-autosave.ts
 *
 * Pure localStorage utilities for compose-screen draft autosave.
 * Every function wraps storage access in try/catch so quota errors and
 * private-browsing restrictions are silently ignored rather than thrown.
 */

import type { EncryptionMode } from './nostr'

export interface ComposeDraft {
  to: string
  cc: string
  subject: string
  body: string
  encryptionMode: EncryptionMode
  showCc: boolean
  savedAt: number
}

/**
 * Versioned wrapper stored in localStorage.
 * Bump DRAFT_VERSION if the shape changes in a breaking way.
 */
const DRAFT_VERSION = 1

interface StoredDraft {
  v: number
  draft: ComposeDraft
}

/**
 * Returns the localStorage key for the current compose context.
 * Fresh compose, reply, and forward each get their own slot so they never
 * clobber each other.
 */
export function composeDraftKey(context: {
  replyId?: string | null
  forwardId?: string | null
}): string {
  if (context.replyId) return `email_draft_reply_${context.replyId}`
  if (context.forwardId) return `email_draft_forward_${context.forwardId}`
  return 'email_draft_compose'
}

/**
 * Serializes `draft` to localStorage under `key`.
 * Fails silently on storage errors.
 */
export function saveDraft(key: string, draft: ComposeDraft): void {
  try {
    const stored: StoredDraft = { v: DRAFT_VERSION, draft }
    localStorage.setItem(key, JSON.stringify(stored))
  } catch {
    // Storage unavailable (private mode, quota) — fail silently.
  }
}

/**
 * Loads and validates a draft from localStorage.
 * Returns null if the slot is empty, the data is malformed, or the version
 * does not match.
 */
export function loadDraft(key: string): ComposeDraft | null {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return null

    const parsed = JSON.parse(raw) as StoredDraft
    if (parsed.v !== DRAFT_VERSION) return null

    const d = parsed.draft
    if (
      typeof d.to !== 'string' ||
      typeof d.cc !== 'string' ||
      typeof d.subject !== 'string' ||
      typeof d.body !== 'string' ||
      typeof d.savedAt !== 'number'
    ) {
      return null
    }
    return d
  } catch {
    return null
  }
}

/**
 * Removes the draft from localStorage.
 * Call this after a successful send.
 */
export function clearDraft(key: string): void {
  try {
    localStorage.removeItem(key)
  } catch {
    // Fail silently.
  }
}

/**
 * Returns true if the draft has at least one non-empty, meaningful field.
 * Used to avoid saving or restoring empty drafts.
 */
export function hasDraftContent(draft: ComposeDraft): boolean {
  return !!(draft.to.trim() || draft.subject.trim() || draft.body.trim())
}
