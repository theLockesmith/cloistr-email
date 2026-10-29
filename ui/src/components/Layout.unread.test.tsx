// @vitest-environment jsdom
/**
 * The Inbox nav item shows how many Inbox messages are unread, from
 * GET /api/v2/email/stats. Before this there was no count anywhere: new mail
 * was only visible by opening the Inbox.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const PUBKEY = 'ab'.repeat(32)
let inboxUnread = 0

vi.mock('@cloistr/ui/components', async (importOriginal) => {
  const real = await importOriginal<typeof import('@cloistr/ui/components')>()
  return {
    ...real,
    useBackendAuth: () => ({
      isAuthenticated: () => true,
      user: { pubkey: PUBKEY },
      logout: () => {},
    }),
  }
})

vi.mock('../lib/api', async (orig) => {
  const real = await orig<typeof import('../lib/api')>()
  return {
    ...real,
    emailAPI: {
      ...real.emailAPI,
      stats: async () => ({ data: { inbox_unread: inboxUnread } }),
    },
  }
})

async function renderLayout() {
  const { default: Layout } = await import('./Layout')
  const { AuthProvider } = await import('@cloistr/ui/auth')
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <MemoryRouter initialEntries={['/inbox']}>
          <Layout />
        </MemoryRouter>
      </AuthProvider>
    </QueryClientProvider>,
  )
}

describe('Inbox unread badge', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('{"names":{}}', { status: 200 }))))
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it('shows the unread count on the Inbox item', async () => {
    inboxUnread = 3
    await renderLayout()
    const nav = screen.getByLabelText('Mail navigation')
    const badge = await within(nav).findByLabelText('3 unread')
    expect(badge.textContent).toBe('3')
    expect(badge.closest('a')?.getAttribute('href')).toBe('/inbox')
  })

  it('caps the badge at 99+', async () => {
    inboxUnread = 250
    await renderLayout()
    const badge = await screen.findByLabelText('250 unread')
    expect(badge.textContent).toBe('99+')
  })

  it('shows no badge when nothing is unread', async () => {
    inboxUnread = 0
    await renderLayout()
    await screen.findByLabelText('Mail navigation')
    await new Promise((r) => setTimeout(r, 50))
    expect(screen.queryByLabelText(/unread$/)).toBeNull()
  })
})
