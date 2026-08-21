import { describe, it, expect } from 'vitest'
import { quoteBody, forwardBlock, replySubject, forwardSubject } from './email-compose'

describe('replySubject', () => {
  it('prepends Re: when not already present', () => {
    expect(replySubject('Hello world')).toBe('Re: Hello world')
  })

  it('does not double-prefix when already Re:', () => {
    expect(replySubject('Re: Hello world')).toBe('Re: Hello world')
  })

  it('handles empty subject', () => {
    expect(replySubject('')).toBe('Re: ')
  })
})

describe('forwardSubject', () => {
  it('prepends Fwd: when not already present', () => {
    expect(forwardSubject('Hello world')).toBe('Fwd: Hello world')
  })

  it('does not double-prefix when already Fwd:', () => {
    expect(forwardSubject('Fwd: Hello world')).toBe('Fwd: Hello world')
  })
})

describe('quoteBody', () => {
  it('prefixes every line with >', () => {
    const result = quoteBody('alice@example.com', '2026-08-20', 'line1\nline2')
    expect(result).toContain('> line1')
    expect(result).toContain('> line2')
  })

  it('includes the sender and date in the attribution line', () => {
    const result = quoteBody('alice@example.com', '2026-08-20', 'hello')
    expect(result).toContain('alice@example.com')
    expect(result).toContain('2026-08-20')
  })

  it('handles empty body without throwing', () => {
    expect(() => quoteBody('a@b.com', '2026-01-01', '')).not.toThrow()
    const result = quoteBody('a@b.com', '2026-01-01', '')
    expect(result).toContain('> ')
  })

  it('returns a string starting with a newline so cursor lands above the quote', () => {
    const result = quoteBody('a@b.com', '2026-01-01', 'text')
    expect(result.startsWith('\n')).toBe(true)
  })
})

describe('forwardBlock', () => {
  it('includes original sender, date, subject, and body', () => {
    const result = forwardBlock('alice@example.com', '2026-08-20', 'Original Subject', 'original body')
    expect(result).toContain('alice@example.com')
    expect(result).toContain('2026-08-20')
    expect(result).toContain('Original Subject')
    expect(result).toContain('original body')
  })

  it('contains the forwarded-message header', () => {
    const result = forwardBlock('a@b.com', '2026-01-01', 'S', 'body')
    expect(result).toContain('Forwarded message')
  })

  it('handles empty body', () => {
    expect(() => forwardBlock('a@b.com', '2026-01-01', 'S', '')).not.toThrow()
  })
})
