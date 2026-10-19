import { describe, it, expect } from 'vitest'
import { pooledSigner } from './signer-pool'
import type { ClientModeSigner } from './client-signer'

// A signer whose calls finish only when the test says so, counting how many
// are in flight at the underlying signer.
function controlledSigner() {
  let inFlight = 0
  let maxInFlight = 0
  const pending: Array<() => void> = []
  const call = () =>
    new Promise<string>((resolve) => {
      inFlight++
      maxInFlight = Math.max(maxInFlight, inFlight)
      pending.push(() => {
        inFlight--
        resolve('ok')
      })
    })
  const signer: ClientModeSigner = {
    getPublicKey: async () => 'pk',
    nip44Encrypt: call,
    nip44Decrypt: call,
    signEvent: call as unknown as ClientModeSigner['signEvent'],
  }
  return { signer, pending, maxInFlight: () => maxInFlight, inFlight: () => inFlight }
}

const tick = () => new Promise((r) => setTimeout(r, 0))

describe('pooledSigner', () => {
  // Review on !172: finishing a call decremented the count and THEN woke a
  // waiter, which re-counted itself only when its microtask ran. A new call
  // arriving in between took the free slot too, so the pool ran cap + 1.
  it('a burst that arrives as a call completes never exceeds the cap', async () => {
    // The window is a few microtasks wide and depends on how the promises are
    // chained, so new calls are issued at every offset across it.
    for (let offset = 0; offset < 12; offset++) {
      const cap = 2
      const s = controlledSigner()
      const pool = pooledSigner(s.signer, cap)
      const all: Promise<unknown>[] = []
      for (let i = 0; i < cap + 1; i++) all.push(pool.nip44Encrypt('p', 'x')) // cap running, 1 waiting
      await tick()
      expect(s.inFlight()).toBe(cap)

      s.pending.shift()!() // one call finishes...
      for (let m = 0; m < offset; m++) await Promise.resolve()
      for (let i = 0; i < 3; i++) all.push(pool.nip44Encrypt('p', 'x')) // ...and a burst arrives
      await tick()
      expect(s.maxInFlight(), `burst ${offset} microtasks after a completion`).toBeLessThanOrEqual(cap)

      while (s.pending.length) {
        s.pending.shift()!()
        await tick()
        expect(s.inFlight()).toBeLessThanOrEqual(cap)
      }
      await Promise.all(all)
    }
  })

  it('runs calls in arrival order once slots free up', async () => {
    const s = controlledSigner()
    const pool = pooledSigner(s.signer, 1)
    const order: number[] = []
    const calls = [0, 1, 2].map((i) => pool.nip44Encrypt('p', 'x').then(() => order.push(i)))
    for (let i = 0; i < 3; i++) {
      await tick()
      s.pending.shift()!()
    }
    await Promise.all(calls)
    expect(order).toEqual([0, 1, 2])
  })
})
