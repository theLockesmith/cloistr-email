// @vitest-environment jsdom
/**
 * CC in client-side (end-to-end) mode.
 *
 * The shipped ComposePage discovered a key for To only, encrypted for To only
 * and passed CC as plain addresses: CC got the same ciphertext and no key to
 * open it. This drives the real page (To + CC, client mode, Send) and checks
 * who it encrypts for, and that a CC with no key stops the send by name.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { nip44, nip04, generateSecretKey, getPublicKey, finalizeEvent } from 'nostr-tools'
import type { SignerInterface } from '@cloistr/auth'
import type { SendEmailRequest } from '../lib/api'

function keySigner() {
  const sk = generateSecretKey()
  const pk = getPublicKey(sk)
  const signer = {
    getPublicKey: async () => pk,
    signEvent: async (ev: Parameters<typeof finalizeEvent>[0]) => finalizeEvent(ev, sk),
    encrypt: async (peer: string, pt: string) => nip04.encrypt(sk, peer, pt),
    decrypt: async (peer: string, ct: string) => nip04.decrypt(sk, peer, ct),
    nip44Encrypt: async (peer: string, pt: string) => nip44.encrypt(pt, nip44.getConversationKey(sk, peer)),
    nip44Decrypt: async (peer: string, ct: string) => nip44.decrypt(ct, nip44.getConversationKey(sk, peer)),
  } as unknown as SignerInterface
  return { signer, pk }
}

const sender = keySigner()
const bob = keySigner() // To
const carol = keySigner() // CC with a discoverable key
const KEYS: Record<string, string> = { 'bob@cloistr.xyz': bob.pk, 'carol@cloistr.xyz': carol.pk }

const sent: SendEmailRequest[] = []
const discovered: string[] = []
const encryptCalls: Array<Record<string, string>> = []

vi.mock('@cloistr/auth', async (orig) => ({
  ...(await orig<typeof import('@cloistr/auth')>()),
  useNostrAuth: () => ({ signer: sender.signer }),
}))
vi.mock('../lib/email-compose', async (orig) => ({
  ...(await orig<typeof import('../lib/email-compose')>()),
  // jsdom's typed arrays come from another realm and libsodium rejects them,
  // so the crypto runs in ComposePage.cc.crypto.test.ts. Here we record who
  // ComposePage asks to encrypt for.
  buildClientModeSend: async (_msg: unknown, recipients: Record<string, string>) => {
    encryptCalls.push(recipients)
    return { subject: 'Encrypted message', body: undefined, pre_encrypted_body: '{}', recipient_pubkeys: recipients }
  },
}))
vi.mock('../lib/api', async (orig) => ({
  ...(await orig<typeof import('../lib/api')>()),
  emailAPI: {
    send: async (req: SendEmailRequest) => (sent.push(req), { data: { status: 'pending' } }),
    get: async () => ({ data: null }),
  },
  keyAPI: {
    discover: async (email: string) => (
      discovered.push(email),
      { data: KEYS[email] ? { email, found: true, pubkey: KEYS[email] } : { email, found: false } }
    ),
  },
}))

beforeEach(() => {
  sent.length = 0
  discovered.length = 0
  encryptCalls.length = 0
  localStorage.clear()
})
afterEach(cleanup)

async function composeClientMode(cc: string) {
  const { default: ComposePage } = await import('./ComposePage')
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={['/compose']}>
        <ComposePage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  fireEvent.change(screen.getByPlaceholderText('recipient@example.com'), { target: { value: 'bob@cloistr.xyz' } })
  fireEvent.click(screen.getByText('Add CC'))
  fireEvent.change(screen.getByPlaceholderText('email1@example.com, email2@example.com'), { target: { value: cc } })
  fireEvent.change(screen.getByPlaceholderText('Email subject'), { target: { value: 'Board minutes' } })
  fireEvent.change(screen.getByPlaceholderText('Write your message...'), { target: { value: 'for everyone' } })
  const clientRadio = document.querySelector('input[type=radio][value=client]') as HTMLInputElement
  await waitFor(() => expect(clientRadio.disabled).toBe(false), { timeout: 3000 })
  fireEvent.click(clientRadio)
  fireEvent.click(screen.getByText('Send'))
}

describe('CC in client mode', () => {
  it('discovers CC keys and encrypts for To and every CC', async () => {
    await composeClientMode('carol@cloistr.xyz')
    await waitFor(() => expect(sent).toHaveLength(1), { timeout: 3000 })

    expect(discovered).toContain('carol@cloistr.xyz')
    expect(encryptCalls).toEqual([{ 'bob@cloistr.xyz': bob.pk, 'carol@cloistr.xyz': carol.pk }])
    expect(sent[0].cc).toEqual(['carol@cloistr.xyz'])
    expect(sent[0].recipient_pubkeys).toEqual({ 'bob@cloistr.xyz': bob.pk, 'carol@cloistr.xyz': carol.pk })
  })

  it('refuses to send when a CC has no key, and says who', async () => {
    await composeClientMode('carol@cloistr.xyz, nokey@example.com')
    await screen.findByText(/nokey@example\.com/, undefined, { timeout: 3000 })

    expect(sent).toHaveLength(0)
    expect(encryptCalls).toHaveLength(0)
    // Named in a plain error, not the signer-recovery panel: nothing is wrong
    // with the user's session.
    expect(screen.queryByText(/still signed in/i)).toBeNull()
    expect(screen.getByText(/nokey@example\.com/).textContent).not.toMatch(/carol@cloistr\.xyz/)
  })
})
