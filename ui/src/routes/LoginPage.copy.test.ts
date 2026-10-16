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

const source = readFileSync(fileURLToPath(new URL('./LoginPage.tsx', import.meta.url)), 'utf8')

describe('landing page copy', () => {
  it('does not claim encryption is on by default', () => {
    expect(source).not.toMatch(/encrypted by default/i)
    expect(source).not.toMatch(/end-to-end encrypted/i)
  })

  it('does not claim the address receives Lightning payments', () => {
    expect(source).not.toMatch(/lightning/i)
  })
})
