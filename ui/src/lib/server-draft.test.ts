/**
 * Server-side drafts: what the compose screen sends to POST/PUT
 * /api/v2/email/drafts, and how it reopens one.
 *
 * Unencrypted drafts go as written (like an unencrypted sent copy). An
 * encrypted draft is sealed in the browser to the user's own key, with the
 * real subject inside the envelope, so the server never holds its plaintext.
 * Without a signer able to do that, there is no server draft at all.
 */
import { describe, expect, it } from 'vitest'
import { nip44, nip04, generateSecretKey, getPublicKey, finalizeEvent } from 'nostr-tools'
import type { SignerInterface } from '@cloistr/auth'
import { buildServerDraft, openServerDraft } from './server-draft'
import type { Email } from './api'

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

const draft = {
  to: 'bob@cloistr.xyz',
  cc: 'carol@cloistr.xyz, dave@example.org',
  subject: 'Merger terms',
  body: 'Offer is 4.2M, do not forward.',
}

describe('buildServerDraft', () => {
  it('sends an unencrypted draft as written', async () => {
    const req = await buildServerDraft({ ...draft, encryptionMode: 'none' }, null)
    expect(req).toEqual({
      to: 'bob@cloistr.xyz',
      cc: ['carol@cloistr.xyz', 'dave@example.org'],
      subject: 'Merger terms',
      body: 'Offer is 4.2M, do not forward.',
      encryption_mode: 'none',
    })
  })

  for (const mode of ['client', 'server'] as const) {
    it(`seals a ${mode}-mode draft to the user only, subject inside`, async () => {
      const me = keySigner()
      const req = await buildServerDraft({ ...draft, encryptionMode: mode }, me.signer)
      expect(req).not.toBeNull()
      expect(req!.encryption_mode).toBe('client')
      expect(req!.subject).toBe('Encrypted message')
      expect(req!.body).not.toContain('4.2M')
      expect(req!.body).not.toContain('Merger')
      const env = JSON.parse(req!.body)
      expect(env.type).toBe('body')
      expect(Object.keys(env.keys)).toEqual([me.pk])
    })
  }

  it('keeps an encrypted draft local when there is no signer', async () => {
    expect(await buildServerDraft({ ...draft, encryptionMode: 'client' }, null)).toBeNull()
  })
})

describe('openServerDraft', () => {
  it('round-trips a sealed draft with the real subject', async () => {
    const me = keySigner()
    const req = await buildServerDraft({ ...draft, encryptionMode: 'client' }, me.signer)
    const stored = {
      id: 'd1',
      to: [req!.to],
      cc: req!.cc,
      subject: req!.subject,
      encrypted_body: req!.body,
      is_encrypted: true,
      encryption_mode: 'client',
      requires_client_decryption: true,
      sender_pubkey: me.pk,
    } as unknown as Email
    expect(await openServerDraft(stored, me.signer)).toEqual({ ...draft, encryptionMode: 'client' })
  })

  it('opens an unencrypted draft without a signer', async () => {
    const stored = {
      id: 'd2', to: ['bob@cloistr.xyz'], cc: ['carol@cloistr.xyz', 'dave@example.org'],
      subject: 'Merger terms', body: 'Offer is 4.2M, do not forward.', is_encrypted: false, encryption_mode: 'none',
    } as unknown as Email
    expect(await openServerDraft(stored, null)).toEqual({ ...draft, encryptionMode: 'none' })
  })

  it('refuses to open a sealed draft without a signer', async () => {
    const stored = { id: 'd3', to: [], subject: 'Encrypted message', encrypted_body: '{}', is_encrypted: true, requires_client_decryption: true } as unknown as Email
    await expect(openServerDraft(stored, null)).rejects.toThrow()
  })
})
