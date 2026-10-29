// @vitest-environment jsdom
/**
 * Server-side drafts on the compose screen.
 *
 * Drafts used to live only in this browser's localStorage, so a draft started
 * on one device was not in the Drafts folder anywhere else. The page now
 * saves to the server a few seconds after typing stops: the first save creates
 * the draft, later saves update that same draft, opening /compose?draft=<id>
 * resumes it, and a successful send deletes it.
 *
 * Mode safety: compose starts in the user's preferred encryption mode and
 * saves nothing to the server until that preference is known. A draft that
 * was saved as plaintext and then switched to encrypted is overwritten with
 * its sealed form at once, so the server is not left holding the plaintext.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComposeFields, ServerDraftRequest } from '../lib/server-draft'

const created: ServerDraftRequest[] = []
const updated: Array<{ id: string; req: ServerDraftRequest }> = []
const deleted: string[] = []
const fetched: string[] = []
// What the capabilities endpoint answers with; a pending promise models a
// preference that has not loaded yet.
let capabilities: () => Promise<{ data: { preferred_mode: string } }>
// When false, buildServerDraft behaves as if no signer can seal (returns null).
let canSeal = true
let recipientHasKey = false

vi.mock('@cloistr/auth', async (orig) => ({
  ...(await orig<typeof import('@cloistr/auth')>()),
  useNostrAuth: () => ({ signer: null }),
}))
vi.mock('../lib/server-draft', async (orig) => ({
  ...(await orig<typeof import('../lib/server-draft')>()),
  // The sealing itself is covered in lib/server-draft.test.ts (node env).
  // Encrypted drafts come back as an opaque stand-in for the envelope.
  buildServerDraft: async (f: ComposeFields): Promise<ServerDraftRequest | null> => {
    if (f.encryptionMode === 'none') return { to: f.to, subject: f.subject, body: f.body, encryption_mode: 'none' }
    if (!canSeal) return null
    return { to: f.to, subject: 'Encrypted message', body: 'SEALED', encryption_mode: 'client' }
  },
  openServerDraft: async (): Promise<ComposeFields> => ({
    to: 'resumed@cloistr.xyz', cc: '', subject: 'Resumed subject', body: 'Resumed body', encryptionMode: 'none',
  }),
}))
vi.mock('../lib/api', async (orig) => ({
  ...(await orig<typeof import('../lib/api')>()),
  emailAPI: {
    createDraft: async (req: ServerDraftRequest) => (created.push(req), { data: { id: 'srv-1', updated_at: '' } }),
    updateDraft: async (id: string, req: ServerDraftRequest) => (updated.push({ id, req }), { data: { id, updated_at: '' } }),
    delete: async (id: string) => (deleted.push(id), { data: {} }),
    get: async (id: string) => (fetched.push(id), { data: { id, to: ['resumed@cloistr.xyz'], subject: 'Resumed subject', body: 'Resumed body', is_encrypted: false } }),
    send: async () => ({ data: { status: 'sent' } }),
  },
  keyAPI: {
    discover: async (email: string) =>
      recipientHasKey ? { data: { email, found: true, pubkey: 'ab'.repeat(32) } } : { data: { email, found: false } },
  },
  encryptionAPI: { getCapabilities: () => capabilities() },
}))

beforeEach(() => {
  created.length = 0
  updated.length = 0
  deleted.length = 0
  fetched.length = 0
  capabilities = async () => ({ data: { preferred_mode: 'none' } })
  canSeal = true
  recipientHasKey = false
  localStorage.clear()
})
afterEach(cleanup)

async function renderAt(path: string) {
  const { default: ComposePage } = await import('./ComposePage')
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={[path]}>
        <ComposePage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const WAIT = { timeout: 8000 }

describe('server drafts', () => {
  it('creates the draft once, then updates the same draft', async () => {
    await renderAt('/compose')
    fireEvent.change(screen.getByPlaceholderText('recipient@example.com'), { target: { value: 'bob@cloistr.xyz' } })
    fireEvent.change(screen.getByPlaceholderText('Email subject'), { target: { value: 'Quarterly' } })
    fireEvent.change(screen.getByPlaceholderText('Write your message...'), { target: { value: 'first' } })
    await waitFor(() => expect(created).toHaveLength(1), WAIT)
    expect(created[0]).toMatchObject({ to: 'bob@cloistr.xyz', subject: 'Quarterly', body: 'first' })

    fireEvent.change(screen.getByPlaceholderText('Write your message...'), { target: { value: 'second' } })
    await waitFor(() => expect(updated.length).toBeGreaterThan(0), WAIT)
    expect(created).toHaveLength(1)
    expect(updated[updated.length - 1]).toMatchObject({ id: 'srv-1', req: { body: 'second' } })
  }, 20000)

  it('resumes /compose?draft=<id> and saves back to it', async () => {
    await renderAt('/compose?draft=abc')
    await waitFor(() => expect((screen.getByPlaceholderText('Email subject') as HTMLInputElement).value).toBe('Resumed subject'), WAIT)
    expect(fetched).toContain('abc')
    expect((screen.getByPlaceholderText('recipient@example.com') as HTMLInputElement).value).toBe('resumed@cloistr.xyz')

    fireEvent.change(screen.getByPlaceholderText('Write your message...'), { target: { value: 'edited' } })
    await waitFor(() => expect(updated.length).toBeGreaterThan(0), WAIT)
    expect(created).toHaveLength(0)
    expect(updated[updated.length - 1]).toMatchObject({ id: 'abc', req: { body: 'edited' } })
  }, 20000)

  it('deletes the server draft after a successful send', async () => {
    await renderAt('/compose?draft=abc')
    await waitFor(() => expect((screen.getByPlaceholderText('Email subject') as HTMLInputElement).value).toBe('Resumed subject'), WAIT)
    fireEvent.click(screen.getByText('Send'))
    await waitFor(() => expect(deleted).toEqual(['abc']), WAIT)
  }, 20000)
})

/** Every request body the server was ever sent for this draft. */
function everySavedBody(): string[] {
  return [...created.map((r) => r.body), ...updated.map((u) => u.req.body)]
}

describe('server drafts: encryption mode', () => {
  it('starts in the preferred encrypted mode and never saves plaintext', async () => {
    capabilities = async () => ({ data: { preferred_mode: 'e2e' } })
    await renderAt('/compose')
    await waitFor(() => expect((screen.getByDisplayValue('client') as HTMLInputElement).checked).toBe(true), WAIT)
    fireEvent.change(screen.getByPlaceholderText('Write your message...'), { target: { value: 'secret plan' } })
    await waitFor(() => expect(created).toHaveLength(1), WAIT)
    expect(created[0]).toMatchObject({ encryption_mode: 'client', body: 'SEALED' })
    expect(everySavedBody()).not.toContain('secret plan')
  }, 20000)

  it('saves nothing to the server until the preference is known', async () => {
    capabilities = () => new Promise(() => {})
    await renderAt('/compose')
    fireEvent.change(screen.getByPlaceholderText('Write your message...'), { target: { value: 'secret plan' } })
    await new Promise((r) => setTimeout(r, 4500))
    expect(created).toHaveLength(0)
    expect(updated).toHaveLength(0)
  }, 20000)

  it('type then switch: a plaintext draft is overwritten with its sealed form at once', async () => {
    recipientHasKey = true
    await renderAt('/compose')
    fireEvent.change(screen.getByPlaceholderText('recipient@example.com'), { target: { value: 'bob@cloistr.xyz' } })
    fireEvent.change(screen.getByPlaceholderText('Write your message...'), { target: { value: 'secret plan' } })
    await waitFor(() => expect(created).toHaveLength(1), WAIT)
    expect(created[0]).toMatchObject({ encryption_mode: 'none', body: 'secret plan' })

    await waitFor(() => expect((screen.getByDisplayValue('server') as HTMLInputElement).disabled).toBe(false), WAIT)
    const switchedAt = Date.now()
    fireEvent.click(screen.getByDisplayValue('server'))
    await waitFor(() => expect(updated.length).toBeGreaterThan(0), WAIT)
    // Not on the 3 s typing debounce: the plaintext copy is replaced now.
    expect(Date.now() - switchedAt).toBeLessThan(1500)
    expect(updated[updated.length - 1]).toMatchObject({ id: 'srv-1', req: { encryption_mode: 'client', body: 'SEALED' } })
  }, 20000)

  it('type then switch before the first save: no plaintext is ever sent', async () => {
    recipientHasKey = true
    await renderAt('/compose')
    fireEvent.change(screen.getByPlaceholderText('recipient@example.com'), { target: { value: 'bob@cloistr.xyz' } })
    fireEvent.change(screen.getByPlaceholderText('Write your message...'), { target: { value: 'secret plan' } })
    await waitFor(() => expect((screen.getByDisplayValue('server') as HTMLInputElement).disabled).toBe(false), WAIT)
    fireEvent.click(screen.getByDisplayValue('server'))
    await waitFor(() => expect(created).toHaveLength(1), WAIT)
    expect(created[0]).toMatchObject({ encryption_mode: 'client', body: 'SEALED' })
    expect(everySavedBody()).not.toContain('secret plan')
  }, 20000)

  it('switching to encrypted with no signer to seal scrubs the plaintext copy', async () => {
    recipientHasKey = true
    canSeal = false
    await renderAt('/compose')
    fireEvent.change(screen.getByPlaceholderText('recipient@example.com'), { target: { value: 'bob@cloistr.xyz' } })
    fireEvent.change(screen.getByPlaceholderText('Email subject'), { target: { value: 'Merger' } })
    fireEvent.change(screen.getByPlaceholderText('Write your message...'), { target: { value: 'secret plan' } })
    await waitFor(() => expect(created).toHaveLength(1), WAIT)

    await waitFor(() => expect((screen.getByDisplayValue('server') as HTMLInputElement).disabled).toBe(false), WAIT)
    fireEvent.click(screen.getByDisplayValue('server'))
    await waitFor(() => expect(updated.length).toBeGreaterThan(0), WAIT)
    const last = updated[updated.length - 1]
    expect(last.id).toBe('srv-1')
    expect(last.req.body).toBe('')
    expect(last.req.subject).toBe('')
  }, 20000)
})
