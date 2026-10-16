import { describe, it, expect } from 'vitest'
import { wrapProtectedHeaders, parseProtectedHeaders } from './protected-headers'

describe('protected headers', () => {
  it('round-trips the subject and body', () => {
    const p = parseProtectedHeaders(wrapProtectedHeaders('Q3 salaries', 'Hi,\n\nsee attached.\n'))
    expect(p.subject).toBe('Q3 salaries')
    expect(p.body).toBe('Hi,\n\nsee attached.\n')
  })

  it('leaves a body that merely starts with "Subject:" alone', () => {
    const text = 'Subject: not a header\n\nbody'
    expect(parseProtectedHeaders(text)).toEqual({ body: text })
  })

  it('leaves older messages without the block unchanged', () => {
    expect(parseProtectedHeaders('plain old body')).toEqual({ body: 'plain old body' })
  })

  it('folds a pasted multi-line subject so it cannot inject headers or end the block', () => {
    const p = parseProtectedHeaders(wrapProtectedHeaders('one\r\n\r\nX-Evil: 1', 'body'))
    expect(p.subject).toBe('one X-Evil: 1')
    expect(p.body).toBe('body')
  })
})
