import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { FEATURES } from './LoginPage'

// The landing page promised "Encrypted by default ... the server never sees
// plaintext", but Compose starts every message with no encryption and the
// server-side mode lets the server encrypt (so it sees the plaintext). The page
// must describe what a new user actually gets.
describe('landing page encryption copy', () => {
  const text = FEATURES.map((f) => `${f.title} ${f.body}`).join(' ')
  const compose = readFileSync(join(__dirname, 'ComposePage.tsx'), 'utf8')

  it('compose still starts with no encryption (the premise of this copy)', () => {
    expect(compose).toMatch(/useState<EncryptionMode>\('none'\)/)
  })

  it('does not claim encryption is on by default', () => {
    expect(text).not.toMatch(/by default/i)
  })

  it('does not promise the server never sees plaintext for all Cloistr mail', () => {
    expect(text).not.toMatch(/server never sees plaintext/i)
  })

  it('still tells users encryption is available and that they choose it', () => {
    expect(text).toMatch(/end-to-end encrypt/i)
    expect(text).toMatch(/choose|turn (it )?on/i)
  })
})
