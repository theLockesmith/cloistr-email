/**
 * signer-pool.ts
 *
 * Caps how many signer calls are in flight at once. Client-mode sends issue
 * their key wraps and upload signatures concurrently so a remote (NIP-46)
 * signer costs a few rounds of latency rather than one per call; the cap
 * keeps one big send from bursting the shared Cloistr signer with dozens of
 * simultaneous requests. Calls beyond the cap wait their turn in order.
 */
import type { ClientModeSigner } from './client-signer'

/** Default in-flight cap for one send. */
export const DEFAULT_SIGNER_CONCURRENCY = 6

function limiter(max: number) {
  if (!Number.isInteger(max) || max < 1) throw new Error(`signer concurrency must be a positive integer, got ${max}`)
  let active = 0
  const queue: Array<() => void> = []
  return async function run<T>(task: () => Promise<T>): Promise<T> {
    if (active >= max) await new Promise<void>((resolve) => queue.push(resolve))
    active++
    try {
      return await task()
    } finally {
      active--
      queue.shift()?.()
    }
  }
}

/**
 * The same signer, with at most `max` of its round-trip calls (NIP-44
 * encrypt/decrypt, event signing) in flight. getPublicKey is cached by the
 * signers and is not limited.
 */
export function pooledSigner(signer: ClientModeSigner, max = DEFAULT_SIGNER_CONCURRENCY): ClientModeSigner {
  const run = limiter(max)
  return {
    getPublicKey: () => signer.getPublicKey(),
    nip44Encrypt: (pk, pt) => run(() => signer.nip44Encrypt(pk, pt)),
    nip44Decrypt: (pk, ct) => run(() => signer.nip44Decrypt(pk, ct)),
    signEvent: (ev) => run(() => signer.signEvent(ev)),
  }
}
