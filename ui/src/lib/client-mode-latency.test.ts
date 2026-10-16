import { describe, it, expect } from 'vitest'
import { nip44, generateSecretKey, getPublicKey, finalizeEvent } from 'nostr-tools'
import type { SignerInterface } from '@cloistr/auth'
import { buildClientModeSend } from './email-compose'
import { clientModeSigner } from './client-signer'

/**
 * A NIP-46-shaped signer where every call costs a fixed delay, standing in for
 * the round trip to a remote signer. @cloistr/auth's NIP-46 client matches
 * responses by request id, so concurrent requests are supported.
 */
function delayedSigner(delayMs: number) {
  const sk = generateSecretKey()
  const pk = getPublicKey(sk)
  const wait = () => new Promise((r) => setTimeout(r, delayMs))
  let inFlight = 0
  let maxInFlight = 0
  const track = async <T>(f: () => T): Promise<T> => {
    inFlight++
    maxInFlight = Math.max(maxInFlight, inFlight)
    await wait()
    inFlight--
    return f()
  }
  const signer = {
    getPublicKey: async () => pk, // cached by the NIP-46 client after login
    signEvent: (ev: Parameters<SignerInterface['signEvent']>[0]) => track(() => finalizeEvent(ev, sk)),
    encrypt: async () => {
      throw new Error('NIP-04 must not be used')
    },
    decrypt: async () => {
      throw new Error('NIP-04 must not be used')
    },
    nip44Encrypt: (peer: string, pt: string) => track(() => nip44.encrypt(pt, nip44.getConversationKey(sk, peer))),
    nip44Decrypt: (peer: string, ct: string) => track(() => nip44.decrypt(ct, nip44.getConversationKey(sk, peer))),
  } as SignerInterface
  return { signer, maxInFlight: () => maxInFlight }
}

const DELAY = 100

describe('client-mode send latency through a remote signer', () => {
  it('5 recipients and 3 attachments take about two round trips, not 27', async () => {
    const { signer, maxInFlight } = delayedSigner(DELAY)
    const cms = clientModeSigner(signer)!
    const recipients = Object.fromEntries([1, 2, 3, 4, 5].map((i) => [`r${i}@example.com`, getPublicKey(generateSecretKey())]))
    const attachments = [1, 2, 3].map((i) => ({ filename: `f${i}.bin`, data_base64: btoa(`file ${i}`) }))

    // Warm up the blob cipher (libsodium initialises once per process) with a
    // zero-latency signer, so the measurement is signer latency, not setup.
    await buildClientModeSend({ subject: 's', body: 'b', attachments }, recipients, clientModeSigner(delayedSigner(0).signer)!)

    const start = performance.now()
    await buildClientModeSend({ subject: 's', body: 'b', attachments }, recipients, cms)
    const ms = performance.now() - start
    console.info(`wall time, 5 recipients + 3 attachments, ${DELAY} ms per signer call: ${Math.round(ms)} ms; max in flight ${maxInFlight()}`)

    // Sequential this was (R+1)*(1+A)+A = 27 calls, measured at 2919 ms.
    // The critical path is now one wrap round, then each attachment's upload
    // signature: two delays plus local crypto (about 330 ms here). The bound
    // leaves room for slow CI while still failing the sequential version.
    expect(ms).toBeLessThan(DELAY * 8)
    // All 24 wraps (6 people x 4 items) are in flight together.
    expect(maxInFlight()).toBeGreaterThanOrEqual(24)
  })
})
