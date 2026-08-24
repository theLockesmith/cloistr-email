/**
 * search-operators.ts
 *
 * Parses Gmail-style search operator syntax into structured query parameters
 * that the v2 list API understands.
 *
 * Supported operators:
 *   from:alice@example.com
 *   to:bob
 *   subject:hello
 *   has:attachment
 *   before:2024-01-01
 *   after:2023-06-01
 *   is:unread
 *   is:starred
 *   label:work
 *
 * Anything that is not an operator is treated as a full-text search term.
 *
 * Operator values may optionally be quoted: from:"Alice Smith" <alice@example.com>
 * but the implementation strips the quotes and passes the inner value.
 */

import type { EmailListParams } from './api'

export interface ParsedSearchQuery {
  /** API params ready to pass to emailAPI.list() */
  params: EmailListParams
  /** The portions of the query that did not match any operator (free-text) */
  freeText: string
}

/** Return true when `raw` contains at least one search operator token. */
export function hasOperators(raw: string): boolean {
  return /\b(from|to|subject|has|before|after|is|label):/i.test(raw)
}

/**
 * Parse a raw search string into structured API params.
 *
 * The parser is intentionally lenient: unknown operators are absorbed as
 * free-text rather than erroring so that typos still produce useful results.
 */
export function parseSearchQuery(raw: string): ParsedSearchQuery {
  const params: EmailListParams = {}
  const freeTextParts: string[] = []

  // Tokenise: split on whitespace but keep quoted strings together.
  // Regex matches either a key:value pair (with optional quoted value) or a
  // bare word.
  const TOKEN_RE = /(\w+):"([^"]+)"|(\w+):(\S+)|("[^"]+"|\S+)/g

  let match: RegExpExecArray | null
  while ((match = TOKEN_RE.exec(raw)) !== null) {
    const [, keyQ, valQ, key, val, bare] = match

    const opKey = (keyQ || key || '').toLowerCase()
    const opVal = (valQ || val || '').trim().replace(/^"(.*)"$/, '$1')

    if (opKey && opVal !== undefined && opVal !== '') {
      switch (opKey) {
        case 'from':
          params.from = opVal
          break
        case 'to':
          params.to = opVal
          break
        case 'subject':
          // subject: maps to the generic text search (backend searches subject+body)
          freeTextParts.push(opVal)
          break
        case 'has':
          if (opVal === 'attachment') params.has_attachment = true
          break
        case 'before':
          params.before = normalizeDate(opVal)
          break
        case 'after':
          params.after = normalizeDate(opVal)
          break
        case 'is':
          if (opVal === 'unread') params.unread = true
          else if (opVal === 'read') params.unread = false
          else if (opVal === 'starred') params.starred = true
          break
        case 'label':
          // Label filter: pass as search for now (backend supports label filter
          // but the list API uses the `labels` param which is an array; we
          // encode it as free-text search until the client is updated to pass
          // multiple labels as separate params).
          freeTextParts.push(opVal)
          break
        default:
          // Unknown operator — treat whole token as free text
          freeTextParts.push(match[0])
      }
    } else if (bare) {
      freeTextParts.push(bare.replace(/^"(.*)"$/, '$1'))
    }
  }

  const freeText = freeTextParts.join(' ').trim()
  if (freeText) {
    params.search = freeText
  }

  return { params, freeText }
}

/**
 * Normalize a user-supplied date string.  Accepts YYYY-MM-DD or common slash
 * and dot separators; leaves RFC3339 strings untouched.
 */
function normalizeDate(s: string): string {
  // Already RFC3339 or ISO date
  if (/^\d{4}-\d{2}-\d{2}(T|$)/.test(s)) return s
  // MM/DD/YYYY or DD.MM.YYYY — convert to YYYY-MM-DD
  const slashMatch = s.match(/^(\d{1,2})[/.](\d{1,2})[/.](\d{4})$/)
  if (slashMatch) {
    const [, a, b, y] = slashMatch
    // Heuristic: if the first component is > 12 it must be the day (DD/MM).
    const [m, d] = parseInt(a, 10) > 12 ? [b, a] : [a, b]
    return `${y}-${m.padStart(2, '0')}-${d.padStart(2, '0')}`
  }
  return s
}

/**
 * Re-serialize a ParsedSearchQuery back into a display string for the search
 * input.  Used when the user is shown the current active search.
 */
export function serializeSearchQuery(parsed: ParsedSearchQuery): string {
  const parts: string[] = []
  const p = parsed.params

  if (p.from) parts.push(`from:${p.from}`)
  if (p.to) parts.push(`to:${p.to}`)
  if (p.has_attachment) parts.push('has:attachment')
  if (p.before) parts.push(`before:${p.before}`)
  if (p.after) parts.push(`after:${p.after}`)
  if (p.unread === true) parts.push('is:unread')
  if (p.unread === false) parts.push('is:read')
  if (p.starred) parts.push('is:starred')
  if (p.search) parts.push(p.search)

  return parts.join(' ')
}
