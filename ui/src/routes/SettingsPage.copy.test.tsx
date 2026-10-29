// @vitest-environment jsdom
/**
 * The Nostr Identity section said "Your email is encrypted and verified
 * using your Nostr keypair." Neither half is true of all mail: mail is
 * end-to-end encrypted only when the sender chooses it and every recipient
 * has a Nostr key (everything else is stored as it arrived), and the keypair
 * does not sign outgoing mail; it is what you sign in with. What is checked
 * is the Nostr signature on incoming mail that carries one ("Verified
 * sender"). This pins copy that says only that.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../lib/api', async (orig) => ({
  ...(await orig<typeof import('../lib/api')>()),
  encryptionAPI: {
    getCapabilities: async () => ({ data: { preferred_mode: 'none' } }),
    setPreferredMode: async () => ({ data: {} }),
  },
  keyAPI: { getMyKey: async () => ({ data: {} }) },
}))

afterEach(cleanup)

async function identitySection() {
  const { default: SettingsPage } = await import('./SettingsPage')
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>
        <SettingsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  const heading = await screen.findByText('Nostr Identity')
  return heading.parentElement as HTMLElement
}

describe('Settings: Nostr Identity copy', () => {
  it('does not claim all mail is encrypted and verified by the keypair', async () => {
    const section = await identitySection()
    expect(section.textContent).not.toMatch(/your email is encrypted/i)
    expect(section.textContent).not.toMatch(/verified using your nostr keypair/i)
  })

  it('says what the keypair does and when mail is encrypted', async () => {
    const section = await identitySection()
    const text = section.textContent ?? ''
    expect(text).toContain('You sign in with your Nostr keypair.')
    expect(text).toContain(
      'Mail is end-to-end encrypted only when you choose encryption and every recipient has a Nostr key; other mail, including mail from ordinary email addresses, is stored as it arrived.',
    )
    expect(text).toContain(
      'Incoming mail signed with the sender’s Nostr key is checked and marked “Verified sender”.',
    )
  })
})
