/**
 * Client-side email filter rules — shared types and logic.
 *
 * Rules are persisted in localStorage (no backend). InboxPage reads them after
 * fetch and applies them to the display copy of each email. Actions that write
 * to the server (star, markRead) update the display immediately; the backend is
 * not called here — the user can still commit changes via the normal UI controls.
 *
 * The 'move' action is intentionally a no-op: the backend already filtered by
 * folder before we received the list, so hiding an email from the current view
 * without writing to the server would silently lose it from all views.
 */

import type { Email } from './api'

export interface FilterRule {
  id: string
  name: string
  /** Criteria — all non-empty fields must match; empty string = skip that check */
  from: string
  to: string
  subject: string
  hasAttachment: boolean
  /** Action to perform when the rule matches */
  action: 'label' | 'move' | 'star' | 'markRead'
  /** Destination label/folder name (used by 'label' and 'move' actions) */
  actionValue: string
  enabled: boolean
  createdAt: number
}

export const FILTER_STORAGE_KEY = 'cloistr-email-filter-rules'

export function loadFilterRules(): FilterRule[] {
  try {
    const raw = localStorage.getItem(FILTER_STORAGE_KEY)
    if (!raw) return []
    return JSON.parse(raw) as FilterRule[]
  } catch {
    return []
  }
}

export function saveFilterRules(rules: FilterRule[]): void {
  try {
    localStorage.setItem(FILTER_STORAGE_KEY, JSON.stringify(rules))
  } catch {
    // Private-mode Safari; silently skip
  }
}

const STAR_LABEL = '\\Starred'

function ruleMatches(rule: FilterRule, email: Email): boolean {
  if (rule.from && !email.from.toLowerCase().includes(rule.from.toLowerCase())) return false
  if (rule.to) {
    const toStr = Array.isArray(email.to) ? email.to.join(',') : email.to
    if (!toStr.toLowerCase().includes(rule.to.toLowerCase())) return false
  }
  if (rule.subject && !email.subject.toLowerCase().includes(rule.subject.toLowerCase())) return false
  if (rule.hasAttachment && !email.has_attachments) return false
  return true
}

/**
 * Apply enabled filter rules to a list of emails, returning a new list with
 * display-only mutations applied. The original objects are never mutated.
 */
export function applyFilterRules(emails: Email[], rules: FilterRule[]): Email[] {
  const enabled = rules.filter(r => r.enabled)
  if (enabled.length === 0) return emails

  return emails.map(email => {
    let modified = email
    for (const rule of enabled) {
      if (!ruleMatches(rule, email)) continue
      switch (rule.action) {
        case 'label': {
          const existing = modified.labels ?? []
          if (!existing.includes(rule.actionValue)) {
            modified = { ...modified, labels: [...existing, rule.actionValue] }
          }
          break
        }
        case 'star': {
          const existing = modified.labels ?? []
          if (!existing.includes(STAR_LABEL)) {
            modified = { ...modified, labels: [...existing, STAR_LABEL] }
          }
          break
        }
        case 'markRead': {
          if (!modified.read_at) {
            modified = { ...modified, read_at: new Date().toISOString() }
          }
          break
        }
        case 'move':
          // No-op: the backend already filtered by folder. Hiding emails from
          // the current view without a server write would silently lose them
          // from all views. Backend write support is needed before this is safe.
          break
      }
    }
    return modified
  })
}
