/**
 * client-signer.ts
 *
 * The signer client-side (end-to-end) mode runs on: whatever the session
 * holds. Most users sign in through the Cloistr signer (NIP-46); some use a
 * browser extension (NIP-07). @cloistr/auth's signers for both implement
 * NIP-44 and event signing, so client mode works with either. Before this,
 * client mode called window.nostr directly and was unavailable to NIP-46 users.
 *
 * NIP-44 is required. NIP-04 is never used as a fallback: it has no integrity
 * check, so a tampered key envelope would decrypt to something instead of
 * being refused.
 */
import type { SignerInterface } from '@cloistr/auth'
import { nip07EnvelopeSigner, hasNip44Support } from './nostr'
import type { SignedNostrEvent } from './api'

export interface ClientModeSigner {
  getPublicKey(): Promise<string>
  nip44Encrypt(pubkey: string, plaintext: string): Promise<string>
  nip44Decrypt(pubkey: string, ciphertext: string): Promise<string>
  signEvent(event: { kind: number; created_at: number; tags: string[][]; content: string }): Promise<SignedNostrEvent>
}

/**
 * The session's signer if it can do NIP-44, else the browser extension if it
 * can, else null (client mode unavailable).
 */
export function clientModeSigner(session: SignerInterface | null | undefined): ClientModeSigner | null {
  if (session && session.nip44Encrypt && session.nip44Decrypt) {
    const enc = session.nip44Encrypt.bind(session)
    const dec = session.nip44Decrypt.bind(session)
    return {
      getPublicKey: () => session.getPublicKey(),
      nip44Encrypt: enc,
      nip44Decrypt: dec,
      signEvent: async (ev) =>
        (await session.signEvent({ ...ev, pubkey: await session.getPublicKey() })) as unknown as SignedNostrEvent,
    }
  }
  if (hasNip44Support()) {
    return nip07EnvelopeSigner()
  }
  return null
}
