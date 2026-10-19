/**
 * Companion to ComposePage.cc.test.tsx: the crypto half, run outside jsdom.
 * Takes ComposePage's recipient resolution (To + CC) into the real send
 * builder and asks the question that matters: can the CC recipient open what
 * arrives? The server relays the same body and attachments to every address.
 */
import { describe, expect, it } from 'vitest'
import { nip44, nip04, generateSecretKey, getPublicKey, finalizeEvent } from 'nostr-tools'
import type { SignerInterface } from '@cloistr/auth'
import { buildClientModeSend, resolveClientRecipients, RecipientKeyError } from '../lib/email-compose'
import { clientModeSigner } from '../lib/client-signer'
import { openClientBody } from '../lib/body-crypto'
import { decryptAttachment } from '../lib/attachment-crypto'
import { parseProtectedHeaders } from '../lib/protected-headers'

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
  return { signer, pk, cms: clientModeSigner(signer)! }
}

describe('what a CC recipient receives in client mode', () => {
  it('can open the body, subject and attachments', async () => {
    const sender = keySigner()
    const bob = keySigner() // To
    const carol = keySigner() // CC
    const FILE = 'minutes.pdf bytes'
    const keys: Record<string, string> = { 'carol@cloistr.xyz': carol.pk }

    const recipients = await resolveClientRecipients(
      ['bob@cloistr.xyz', 'carol@cloistr.xyz'],
      { 'bob@cloistr.xyz': bob.pk },
      async (email) => keys[email] ?? null,
    )
    const req = await buildClientModeSend(
      {
        subject: 'Board minutes',
        body: 'for bob and carol',
        attachments: [{ filename: 'minutes.pdf', content_type: 'application/pdf', data_base64: btoa(FILE) }],
      },
      recipients,
      sender.cms,
    )
    const att = atob(req.attachments![0].data_base64)

    for (const r of [bob, carol]) {
      expect(parseProtectedHeaders(await openClientBody(req.pre_encrypted_body!, sender.pk, r.cms, r.cms.nip44Decrypt)))
        .toEqual({ subject: 'Board minutes', body: 'for bob and carol' })
      expect((await decryptAttachment(att, r.cms)).filename).toBe('minutes.pdf')
    }
    // Before the fix: body and attachment keys were for [To, sender] only.
    const holders = Object.keys(JSON.parse(req.pre_encrypted_body!).keys)
    expect(holders.sort()).toEqual([bob.pk, carol.pk, sender.pk].sort())
  })

  it('names every recipient with no key instead of sending', async () => {
    const bob = keySigner()
    const err = await resolveClientRecipients(
      ['bob@cloistr.xyz', 'x@example.com', 'y@example.com'],
      { 'bob@cloistr.xyz': bob.pk },
      async () => null,
    ).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(RecipientKeyError)
    expect((err as RecipientKeyError).missing).toEqual(['x@example.com', 'y@example.com'])
    expect((err as Error).message).toMatch(/x@example\.com, y@example\.com/)
  })
})
