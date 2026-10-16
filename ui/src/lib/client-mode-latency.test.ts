import { describe, it, expect } from 'vitest'
import { nip44, generateSecretKey, getPublicKey, finalizeEvent } from 'nostr-tools'
import type { SignerInterface } from '@cloistr/auth'
import { buildClientModeSend } from './email-compose'
import { clientModeSigner } from './client-signer'
import { DEFAULT_SIGNER_CONCURRENCY } from './signer-pool'

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

const recipients = Object.fromEntries([1, 2, 3, 4, 5].map((i) => [`r${i}@example.com`, getPublicKey(generateSecretKey())]))
const attachments = [1, 2, 3].map((i) => ({ filename: `f${i}.bin`, data_base64: btoa(`file ${i}`) }))
const msg = { subject: 's', body: 'b', attachments }

async function measure(concurrency?: number) {
  // Warm up the blob cipher (libsodium initialises once per process) with a
  // zero-latency signer, so the measurement is signer latency, not setup.
  await buildClientModeSend(msg, recipients, clientModeSigner(delayedSigner(0).signer)!)
  const { signer, maxInFlight } = delayedSigner(DELAY)
  const start = performance.now()
  await buildClientModeSend(msg, recipients, clientModeSigner(signer)!, { signerConcurrency: concurrency })
  return { ms: performance.now() - start, maxInFlight: maxInFlight() }
}

describe('client-mode send latency through a remote signer (5 recipients, 3 attachments)', () => {
  // Sequential this was (R+1)*(1+A)+A = 27 calls, measured at 2919 ms with
  // 100 ms per call. 27 calls at 6 in flight is 5 rounds (~500 ms) plus
  // local crypto. The bound leaves room for slow CI while still failing the
  // sequential version.
  it('default pool: a few rounds, not 27', async () => {
    const { ms, maxInFlight } = await measure()
    console.info(`default pool (${DEFAULT_SIGNER_CONCURRENCY}): ${Math.round(ms)} ms, max in flight ${maxInFlight}`)
    expect(ms).toBeLessThan(DELAY * 12)
  })

  // A big send must not burst the shared signer.
  it.each([1, 3, DEFAULT_SIGNER_CONCURRENCY])('never has more than %i calls in flight', async (cap) => {
    const { ms, maxInFlight } = await measure(cap)
    console.info(`cap ${cap}: ${Math.round(ms)} ms, max in flight ${maxInFlight}`)
    expect(maxInFlight).toBeLessThanOrEqual(cap)
    expect(maxInFlight).toBe(cap) // and the pool is actually used
  })

  it('refuses a nonsensical cap', async () => {
    await expect(buildClientModeSend(msg, recipients, clientModeSigner(delayedSigner(0).signer)!, { signerConcurrency: 0 })).rejects.toThrow(
      /positive integer/,
    )
  })
})
