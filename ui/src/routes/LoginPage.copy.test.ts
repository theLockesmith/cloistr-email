// The logged-out landing page is the only thing a visitor reads before
// signing up, so it must promise only what ships. The WV3 live-feature audit
// (MAIL-51, MAIL-52) found it claiming two things the code does not do:
//   - "Encrypted by default ... the server never sees plaintext": compose
//     defaults to no encryption, and server-side mode lets the server decrypt.
//   - "One address for email, Nostr, and Lightning": nothing in mail receives
//     Lightning payments.
// If either feature ships, update this test together with the page copy.
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8')
const source = read('./LoginPage.tsx')
// Visitors also read the page's meta description (search results, link
// previews) and the compose screen's encryption option.
const surfaces = { landing: source, meta: read('../../index.html'), compose: read('./ComposePage.tsx') }

describe('landing page copy', () => {
  it('does not claim encryption is on by default', () => {
    expect(source).not.toMatch(/encrypted by default/i)
    expect(source).not.toMatch(/end-to-end encrypted/i)
  })

  // Client mode encrypts only the body: subject and recipients travel in the
  // clear, so no surface may say the server sees nothing.
  it.each(Object.entries(surfaces))('%s does not claim the server sees no plaintext', (_, text) => {
    expect(text).not.toMatch(/end-to-end encrypted/i)
    expect(text).not.toMatch(/never sees (the )?plaintext/i)
    expect(text).not.toMatch(/zero-knowledge/i)
  })

  it('does not claim the address receives Lightning payments', () => {
    expect(source).not.toMatch(/lightning/i)
  })
})
