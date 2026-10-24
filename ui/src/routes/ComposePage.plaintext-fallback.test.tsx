// @vitest-environment jsdom
/**
 * "Send without encryption" after an encryption refusal.
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
import { AxiosError, AxiosHeaders } from 'axios'

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
let sendRefusal: { code: string; message: string } | null = null

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
    send: async (req: SendEmailRequest) => {
      sent.push(req)
      if (sendRefusal && req.encryption_mode !== 'none') {
        const config = { headers: new AxiosHeaders() }
        throw new AxiosError('Request failed with status code 400', 'ERR_BAD_REQUEST', config, null, {
          status: 400, statusText: 'Bad Request', headers: {}, config, data: sendRefusal,
        })
      }
      return { data: { status: 'pending' } }
    },
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
  sendRefusal = null
  localStorage.clear()
})
afterEach(cleanup)

// When an encrypted send is refused (no key for a recipient, encryption failed,
// a CC or attachment server-side mode cannot cover), the user used to be left
// with an error and no way forward except retyping the message as plaintext.
// The page now offers "Send without encryption" as an explicit second step. It
// never falls back on its own, and it is offered only for encryption refusals.

async function compose(mode: 'server' | 'client', cc?: string) {
  const { default: ComposePage } = await import('./ComposePage')
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={['/compose']}>
        <ComposePage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  fireEvent.change(screen.getByPlaceholderText('recipient@example.com'), { target: { value: 'bob@cloistr.xyz' } })
  if (cc) {
    fireEvent.click(screen.getByText('Add CC'))
    fireEvent.change(screen.getByPlaceholderText('email1@example.com, email2@example.com'), { target: { value: cc } })
  }
  fireEvent.change(screen.getByPlaceholderText('Email subject'), { target: { value: 'Board minutes' } })
  fireEvent.change(screen.getByPlaceholderText('Write your message...'), { target: { value: 'for everyone' } })
  const radio = document.querySelector(`input[type=radio][value=${mode}]`) as HTMLInputElement
  await waitFor(() => expect(radio.disabled).toBe(false), { timeout: 3000 })
  fireEvent.click(radio)
  fireEvent.click(screen.getByText('Send'))
}

describe('send without encryption after a refusal', () => {
  for (const code of ['SERVER_ENCRYPTION_FAILED', 'SERVER_ENCRYPTION_ONE_RECIPIENT', 'SERVER_ENCRYPTION_NO_ATTACHMENTS', 'RECIPIENT_NOT_ENCRYPTED']) {
    it(`offers it after ${code}, and sends plaintext only when clicked`, async () => {
      sendRefusal = { code, message: 'encryption failed, so the message was NOT sent' }
      await compose('server')
      await screen.findByText(/the message was NOT sent/)
      const plain = await screen.findByRole('button', { name: /send without encryption/i })
      expect(sent).toHaveLength(1) // nothing resent on its own
      expect(sent[0].encryption_mode).toBe('server')

      fireEvent.click(plain)
      await waitFor(() => expect(sent).toHaveLength(2), { timeout: 3000 })
      expect(sent[1].encryption_mode).toBe('none')
      expect(sent[1].recipient_pubkeys).toBeUndefined()
      expect(sent[1].pre_encrypted_body).toBeUndefined()
      expect(sent[1].body).toBe('for everyone')
      expect(sent[1].subject).toBe('Board minutes')
    })
  }

  it('offers it when a client-mode CC has no key, and the plaintext send keeps the CC', async () => {
    await compose('client', 'dave@example.com')
    const plain = await screen.findByRole('button', { name: /send without encryption/i }, { timeout: 3000 })
    expect(sent).toHaveLength(0)
    fireEvent.click(plain)
    await waitFor(() => expect(sent).toHaveLength(1), { timeout: 3000 })
    expect(sent[0].encryption_mode).toBe('none')
    expect(sent[0].cc).toEqual(['dave@example.com'])
    expect(sent[0].pre_encrypted_body).toBeUndefined()
  })

  it('is not offered for errors that are not about encryption', async () => {
    sendRefusal = { code: 'HEADER_INJECTION', message: 'subject must not contain line breaks' }
    await compose('server')
    await screen.findByText(/subject must not contain line breaks/)
    expect(screen.queryByRole('button', { name: /send without encryption/i })).toBeNull()
  })
})
