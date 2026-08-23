/**
 * search-operators.test.ts
 *
 * Source-level structural tests for the search operator parser.
 * These run in Node via vitest without a DOM (no jsdom configured).
 * They are NOT behavioural UI tests — they verify only parsing logic.
 */

import { describe, it, expect } from 'vitest'
import { parseSearchQuery, hasOperators, serializeSearchQuery } from './search-operators'

// ---------------------------------------------------------------------------
// hasOperators
// ---------------------------------------------------------------------------

describe('hasOperators', () => {
  it('returns true when a recognised operator is present', () => {
    expect(hasOperators('from:alice@example.com hello world')).toBe(true)
    expect(hasOperators('has:attachment')).toBe(true)
    expect(hasOperators('is:unread')).toBe(true)
  })

  it('returns false for plain text with no operators', () => {
    expect(hasOperators('hello world')).toBe(false)
    expect(hasOperators('')).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// parseSearchQuery — from:
// ---------------------------------------------------------------------------

describe('parseSearchQuery — from:', () => {
  it('extracts the from address', () => {
    const { params } = parseSearchQuery('from:alice@example.com')
    expect(params.from).toBe('alice@example.com')
  })

  it('does not set search for a pure from: query', () => {
    const { params } = parseSearchQuery('from:alice@example.com')
    expect(params.search).toBeUndefined()
  })
})

// ---------------------------------------------------------------------------
// parseSearchQuery — to:
// ---------------------------------------------------------------------------

describe('parseSearchQuery — to:', () => {
  it('extracts the to address', () => {
    const { params } = parseSearchQuery('to:bob@example.com')
    expect(params.to).toBe('bob@example.com')
  })
})

// ---------------------------------------------------------------------------
// parseSearchQuery — has:attachment
// ---------------------------------------------------------------------------

describe('parseSearchQuery — has:attachment', () => {
  it('sets has_attachment to true', () => {
    const { params } = parseSearchQuery('has:attachment')
    expect(params.has_attachment).toBe(true)
  })

  it('ignores unknown has: values', () => {
    const { params } = parseSearchQuery('has:something')
    expect(params.has_attachment).toBeUndefined()
  })
})

// ---------------------------------------------------------------------------
// parseSearchQuery — before: / after:
// ---------------------------------------------------------------------------

describe('parseSearchQuery — date operators', () => {
  it('extracts before date in ISO format unchanged', () => {
    const { params } = parseSearchQuery('before:2024-01-01')
    expect(params.before).toBe('2024-01-01')
  })

  it('extracts after date', () => {
    const { params } = parseSearchQuery('after:2023-06-15')
    expect(params.after).toBe('2023-06-15')
  })

  it('normalizes MM/DD/YYYY to YYYY-MM-DD', () => {
    const { params } = parseSearchQuery('before:12/31/2023')
    expect(params.before).toBe('2023-12-31')
  })
})

// ---------------------------------------------------------------------------
// parseSearchQuery — is:
// ---------------------------------------------------------------------------

describe('parseSearchQuery — is:', () => {
  it('sets unread to true for is:unread', () => {
    const { params } = parseSearchQuery('is:unread')
    expect(params.unread).toBe(true)
  })

  it('sets unread to false for is:read', () => {
    const { params } = parseSearchQuery('is:read')
    expect(params.unread).toBe(false)
  })

  it('sets starred to true for is:starred', () => {
    const { params } = parseSearchQuery('is:starred')
    expect(params.starred).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// parseSearchQuery — free text
// ---------------------------------------------------------------------------

describe('parseSearchQuery — free text', () => {
  it('treats bare words as search text', () => {
    const { params } = parseSearchQuery('hello world')
    expect(params.search).toBe('hello world')
    expect(params.from).toBeUndefined()
  })

  it('combines multiple operators with free text', () => {
    const { params, freeText } = parseSearchQuery('from:alice@example.com meeting notes')
    expect(params.from).toBe('alice@example.com')
    expect(params.search).toBe('meeting notes')
    expect(freeText).toBe('meeting notes')
  })
})

// ---------------------------------------------------------------------------
// parseSearchQuery — mixed queries
// ---------------------------------------------------------------------------

describe('parseSearchQuery — compound queries', () => {
  it('handles multiple operators in one query', () => {
    const { params } = parseSearchQuery('from:alice to:bob has:attachment is:unread')
    expect(params.from).toBe('alice')
    expect(params.to).toBe('bob')
    expect(params.has_attachment).toBe(true)
    expect(params.unread).toBe(true)
  })

  it('does not produce extra params for an empty input', () => {
    const { params } = parseSearchQuery('')
    expect(Object.keys(params)).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// serializeSearchQuery
// ---------------------------------------------------------------------------

describe('serializeSearchQuery', () => {
  it('round-trips from: and to:', () => {
    const parsed = parseSearchQuery('from:alice to:bob')
    const serialized = serializeSearchQuery(parsed)
    expect(serialized).toContain('from:alice')
    expect(serialized).toContain('to:bob')
  })

  it('includes has:attachment when set', () => {
    const parsed = parseSearchQuery('has:attachment')
    expect(serializeSearchQuery(parsed)).toContain('has:attachment')
  })

  it('includes is:unread when set', () => {
    const parsed = parseSearchQuery('is:unread')
    expect(serializeSearchQuery(parsed)).toContain('is:unread')
  })
})
