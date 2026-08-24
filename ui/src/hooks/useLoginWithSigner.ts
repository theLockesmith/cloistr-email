/**
 * useLoginWithSigner
 *
 * Bridge hook used by LoginPage after @cloistr/ui LoginModal completes.
 *
 * LoginModal (connect mode) resolves authState.signer in @cloistr/auth's
 * context. This hook takes that SignerInterface and runs email's backend
 * challenge/verify to obtain a JWT session, storing it in the shape that
 * BackendAuthProvider.validateToken and the api.ts axios interceptor both
 * expect (validated once on next mount via the token-info endpoint).
 *
 * localStorage contract after success:
 *   access_token    → JWT token  (BackendAuthProvider reads this)
 *   token_expiry    → ISO-8601 string (BackendAuthProvider scheduleTokenRefresh)
 *   user_pubkey     → hex pubkey (BackendAuthProvider + api.ts)
 *   session_token   → JWT token  (api.ts axios interceptor reads this)
 *
 * After storing, the hook triggers a hard navigation to /inbox so
 * BackendAuthProvider re-mounts and calls validateToken with a real Bearer
 * token → setUser({pubkey}) → isAuthenticated() = true.
 */

import { useCallback } from 'react'
import type { SignerInterface } from '@cloistr/auth'
import { withSignerRetry } from '@cloistr/ui'
import { verifyEvent, type UnsignedEvent, type Event as NostrEvent } from 'nostr-tools'

const API_BASE = '/api/v1'

interface ChallengeResponse {
  challenge: string
  nonce: string
}

interface VerifyResponse {
  /** ISO-8601 expiry (BackendAuthProvider-compatible "expires_at" field) */
  expires_at: string
  /** JWT session token */
  access_token: string
  /** Original token field (same value, kept for backward compat) */
  token: string
  /** User sub-object expected by BackendAuthProvider */
  user: { pubkey: string }
}

export function useLoginWithSigner() {
  const loginWithSigner = useCallback(async (signer: SignerInterface): Promise<void> => {
    // Wrap both NIP-46 signing calls in withSignerRetry so a transient relay
    // hiccup (code: NO_RELAYS / CONNECTION_FAILED / DISCONNECTED) is retried
    // up to 3 times before surfacing. A signer refusal (CANCELLED) is never
    // retried — the user said no and re-prompting them would be wrong.
    const pubkey = await withSignerRetry(() => signer.getPublicKey())

    // Step 1: fetch challenge from email backend
    const challengeRes = await fetch(`${API_BASE}/auth/challenge`)
    if (!challengeRes.ok) {
      throw new Error(`Failed to fetch auth challenge: ${challengeRes.status}`)
    }
    const { challenge, nonce } = (await challengeRes.json()) as ChallengeResponse

    // Step 2: build and sign a NIP-98–style auth event (kind 27235)
    const unsignedEvent: UnsignedEvent = {
      kind: 27235,
      created_at: Math.floor(Date.now() / 1000),
      tags: [
        ['challenge', challenge],
        ['nonce', nonce],
      ],
      content: JSON.stringify({ challenge, nonce }),
      pubkey,
    }

    const signedEvent: NostrEvent = await withSignerRetry(() => signer.signEvent(unsignedEvent))

    if (!verifyEvent(signedEvent)) {
      throw new Error('Local signature verification failed')
    }

    // Step 3: verify against email backend → get JWT
    const verifyRes = await fetch(`${API_BASE}/auth/verify`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ signedEvent }),
    })
    if (!verifyRes.ok) {
      const err = await verifyRes.json().catch(() => ({})) as { error?: string }
      throw new Error(err.error ?? `Auth verify failed: ${verifyRes.status}`)
    }
    const result = (await verifyRes.json()) as VerifyResponse

    const token = result.access_token || result.token
    const expiresAt = result.expires_at // ISO-8601 from backend

    if (!token) throw new Error('Auth response missing token')

    // Step 4: persist in localStorage using both naming conventions
    //   BackendAuthProvider.validateToken reads: access_token, token_expiry
    //   api.ts axios interceptor reads:          session_token
    localStorage.setItem('access_token', token)
    localStorage.setItem('token_expiry', expiresAt)
    localStorage.setItem('user_pubkey', pubkey)
    localStorage.setItem('session_token', token)

    // Step 5: hard-navigate to /inbox so BackendAuthProvider re-mounts,
    // calls validateToken (Bearer now present), sets user+token state,
    // and isAuthenticated() returns true.
    window.location.href = '/inbox'
  }, [])

  return { loginWithSigner }
}
