import { describe, it, expect } from 'vitest'
import { nip44, nip04, generateSecretKey, getPublicKey, finalizeEvent, verifyEvent } from 'nostr-tools'
import type { SignerInterface } from '@cloistr/auth'
import { buildClientModeSend } from './email-compose'
import { clientModeSigner } from './client-signer'
import { openClientBody } from './body-crypto'
import { decryptAttachment, ENCRYPTED_ATTACHMENT_FILENAME } from './attachment-crypto'
import { parseProtectedHeaders, ENCRYPTED_SUBJECT_PLACEHOLDER } from './protected-headers'

/**
 * Shaped like @cloistr/auth's NIP-46 (Cloistr signer) session signer, backed
 * by a real secp256k1 key, counting every call that would be a round trip to
 * the remote signer. There is no window.nostr in this environment, so client
 * mode can only work through the session signer.
 */
function nip46LikeSigner() {
  const sk = generateSecretKey()
  const pk = getPublicKey(sk)
  const calls = { getPublicKey: 0, nip44Encrypt: 0, nip44Decrypt: 0, signEvent: 0, nip04: 0 }
  const signer: SignerInterface = {
    getPublicKey: async () => (calls.getPublicKey++, pk),
    signEvent: async (ev) => (calls.signEvent++, finalizeEvent(ev, sk)),
    encrypt: async (peer, pt) => (calls.nip04++, nip04.encrypt(sk, peer, pt)),
    decrypt: async (peer, ct) => (calls.nip04++, nip04.decrypt(sk, peer, ct)),
    nip44Encrypt: async (peer, pt) => (calls.nip44Encrypt++, nip44.encrypt(pt, nip44.getConversationKey(sk, peer))),
    nip44Decrypt: async (peer, ct) => (calls.nip44Decrypt++, nip44.decrypt(ct, nip44.getConversationKey(sk, peer))),
  } as SignerInterface
  return { signer, pk, calls }
}

const FILE = '%PDF-1.7 q3 numbers'

describe('client-mode send through the session signer (NIP-46, no extension)', () => {
  it('is available without a browser extension', () => {
    expect(typeof window).toBe('undefined')
    expect(clientModeSigner(nip46LikeSigner().signer)).not.toBeNull()
  })

  it('is unavailable for a signer without NIP-44 (never falls back to NIP-04)', () => {
    const { signer } = nip46LikeSigner()
    const noNip44 = { ...signer, nip44Encrypt: undefined, nip44Decrypt: undefined } as SignerInterface
    expect(clientModeSigner(noNip44)).toBeNull()
  })

  it('builds a send that leaks nothing, opens for recipient and sender, and is storable', async () => {
    const sender = nip46LikeSigner()
    const recipient = nip46LikeSigner()
    const cms = clientModeSigner(sender.signer)!

    const req = await buildClientModeSend(
      {
        subject: 'Q3 salaries',
        body: 'see attached',
        attachments: [{ filename: 'q3.pdf', content_type: 'application/pdf', data_base64: btoa(FILE) }],
      },
      { 'bob@example.com': recipient.pk },
      cms,
    )

    // Nothing readable in the request.
    const wire = JSON.stringify(req)
    for (const secret of ['Q3 salaries', 'see attached', 'q3.pdf', 'application/pdf', btoa(FILE)]) {
      expect(wire).not.toContain(secret)
    }
    expect(req.subject).toBe(ENCRYPTED_SUBJECT_PLACEHOLDER)
    expect(req.attachments![0].filename).toBe(ENCRYPTED_ATTACHMENT_FILENAME)
    expect(sender.calls.nip04).toBe(0)

    // Round trips to the signer for one recipient and one attachment.
    // body: key wrapped to recipient + sender (2 nip44Encrypt)
    // attachment: key wrapped to recipient + sender (2 nip44Encrypt), upload authorization (1 signEvent)
    expect(sender.calls.nip44Encrypt).toBe(4)
    expect(sender.calls.signEvent).toBe(1)
    console.info('signer round trips for one send (1 recipient, 1 attachment):', sender.calls)

    // Opens for the recipient, real subject included.
    const rcms = clientModeSigner(recipient.signer)!
    const opened = parseProtectedHeaders(await openClientBody(req.pre_encrypted_body!, sender.pk, rcms, rcms.nip44Decrypt))
    expect(opened).toEqual({ subject: 'Q3 salaries', body: 'see attached' })
    const att = await decryptAttachment(atob(req.attachments![0].data_base64), rcms)
    expect(atob(att.data_base64)).toBe(FILE)
    expect(att.filename).toBe('q3.pdf')

    // Opens for the sender too (their own sent copy).
    expect(parseProtectedHeaders(await openClientBody(req.pre_encrypted_body!, sender.pk, cms, cms.nip44Decrypt)).subject).toBe(
      'Q3 salaries',
    )

    // The upload authorization is the sender's own valid signature.
    const auth = req.attachments![0].upload_auth!
    expect(verifyEvent(auth)).toBe(true)
    expect(auth.pubkey).toBe(sender.pk)
  })
})
