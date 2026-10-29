// @vitest-environment jsdom
/**
 * Contacts can be edited and deleted from the Contacts page, and a refused
 * add says why. The page had neither button, and a failed add was silent.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AxiosError, AxiosHeaders } from 'axios'
import type { Contact } from '../lib/api'

let book: Contact[] = []
const updates: Array<[string, Partial<Contact>]> = []
const deletes: string[] = []

function refusal(status: number, code: string, message: string) {
  const config = { headers: new AxiosHeaders() }
  return new AxiosError(`Request failed with status code ${status}`, 'ERR_BAD_REQUEST', config, null, {
    status, statusText: '', headers: {}, config, data: { code, message },
  })
}

vi.mock('../lib/api', async (orig) => ({
  ...(await orig<typeof import('../lib/api')>()),
  contactAPI: {
    list: async () => ({ data: { contacts: book, total: book.length } }),
    add: async (data: Partial<Contact>) => {
      if (book.some((c) => c.email === data.email)) throw refusal(409, 'CONTACT_EXISTS', 'that address is already in your contacts')
      const c = { id: `id-${book.length + 1}`, always_encrypt: false, blocked: false, created_at: '', ...data } as Contact
      book = [...book, c]
      return { data: c }
    },
    update: async (id: string, data: Partial<Contact>) => {
      updates.push([id, data])
      book = book.map((c) => (c.id === id ? { ...c, ...data } : c))
      return { data: book.find((c) => c.id === id) }
    },
    delete: async (id: string) => {
      deletes.push(id)
      book = book.filter((c) => c.id !== id)
      return { data: { status: 'deleted' } }
    },
  },
}))

beforeEach(() => {
  book = [
    { id: 'c1', email: 'carol@example.com', name: 'Carol', always_encrypt: false, blocked: false, created_at: '' },
    { id: 'c2', email: 'dave@example.com', name: 'Dave', always_encrypt: false, blocked: false, created_at: '' },
  ]
  updates.length = 0
  deletes.length = 0
  vi.spyOn(window, 'confirm').mockReturnValue(true)
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

async function renderPage() {
  const { default: ContactsPage } = await import('./ContactsPage')
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>
        <ContactsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return screen.findByText('Carol')
}

function card(name: string) {
  return screen.getByText(name).closest('[data-testid="contact-card"]') as HTMLElement
}

describe('ContactsPage', () => {
  it('edits a contact and shows the saved name', async () => {
    await renderPage()
    const carol = card('Carol')
    fireEvent.click(within(carol).getByRole('button', { name: /edit/i }))
    fireEvent.change(within(carol).getByLabelText(/name/i), { target: { value: 'Carol Danvers' } })
    fireEvent.click(within(carol).getByRole('button', { name: /save/i }))

    await screen.findByText('Carol Danvers')
    expect(updates).toEqual([['c1', expect.objectContaining({ name: 'Carol Danvers', email: 'carol@example.com' })]])
  })

  it('deletes a contact after confirming', async () => {
    await renderPage()
    fireEvent.click(within(card('Dave')).getByRole('button', { name: /delete/i }))
    await waitFor(() => expect(screen.queryByText('Dave')).toBeNull())
    expect(deletes).toEqual(['c2'])
    expect(screen.getByText('Carol')).toBeTruthy()
  })

  it('says why an add was refused', async () => {
    await renderPage()
    fireEvent.click(screen.getByRole('button', { name: /add contact/i }))
    fireEvent.change(screen.getByLabelText(/^email/i), { target: { value: 'carol@example.com' } })
    fireEvent.change(screen.getByLabelText(/^name/i), { target: { value: 'Carol again' } })
    fireEvent.click(screen.getByRole('button', { name: /^add$/i }))
    await screen.findByText(/already in your contacts/i)
  })

  it('adds a contact with no name', async () => {
    await renderPage()
    fireEvent.click(screen.getByRole('button', { name: /add contact/i }))
    fireEvent.change(screen.getByLabelText(/^email/i), { target: { value: 'erin@example.com' } })
    fireEvent.click(screen.getByRole('button', { name: /^add$/i }))
    await screen.findByText('erin@example.com')
  })
})
