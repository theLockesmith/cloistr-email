// @vitest-environment jsdom
/**
 * Opening a message marks it read on the server (GET /api/v2/email/{id} sets
 * read_at for received mail). The page must then refresh the cached Inbox list
 * and unread count, or the message keeps its unread dot and the badge keeps
 * counting it until something else refetches.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@cloistr/auth', async (orig) => ({
  ...(await orig<typeof import('@cloistr/auth')>()),
  useNostrAuth: () => ({ signer: null }),
}))

vi.mock('../lib/api', async (orig) => {
  const real = await orig<typeof import('../lib/api')>()
  return {
    ...real,
    emailAPI: {
      ...real.emailAPI,
      get: async (id: string) => ({
        data: {
          id,
          from: 'alice@example.org',
          to: ['me@cloistr.xyz'],
          subject: 'hello-markread',
          body: 'hi',
          is_encrypted: false,
          folder: 'INBOX',
          created_at: '2026-10-29T10:00:00Z',
          read_at: '2026-10-29T10:05:00Z',
        },
      }),
    },
  }
})

afterEach(cleanup)

describe('EmailPage after opening a message', () => {
  it('refreshes the Inbox list and unread count', async () => {
    const { default: EmailPage } = await import('./EmailPage')
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const invalidate = vi.spyOn(qc, 'invalidateQueries')
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/emails/e1']}>
          <Routes>
            <Route path="/emails/:id" element={<EmailPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    await screen.findByText('hello-markread')
    await waitFor(() =>
      expect(invalidate).toHaveBeenCalledWith(expect.objectContaining({ queryKey: ['emails'] })),
    )
  })
})
