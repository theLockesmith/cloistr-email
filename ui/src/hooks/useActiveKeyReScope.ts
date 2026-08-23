/**
 * useActiveKeyReScope
 *
 * Watches @cloistr/auth's activePubkey. When the user switches to a different
 * key via the Header key-switcher, this hook detects the change and re-auths
 * the mail backend (challenge/verify → new JWT), then invalidates the
 * react-query cache so the mailbox re-fetches for the new identity.
 *
 * Guard: skips if the backend is already scoped to activePubkey (avoids loops
 * on initial mount and redundant re-auth when the key hasn't actually changed).
 */

import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useNostrAuth } from '@cloistr/auth'
import type { SignerInterface } from '@cloistr/auth'
import { withSignerRetry } from '@cloistr/ui'
import { verifyEvent, type UnsignedEvent, type Event as NostrEvent } from 'nostr-tools'

const API_BASE = '/api/v1'

interface ChallengeResponse {
  challenge: string
  nonce: string
}

interface VerifyResponse {
  expires_at: string
  access_token: string
  token?: string
  user: { pubkey: string }
}

async function reAuthForKey(signer: SignerInterface): Promise<string> {
  // Both NIP-46 calls are wrapped in withSignerRetry. A transient relay
  // failure (NO_RELAYS / CONNECTION_FAILED / DISCONNECTED) is retried up to
  // 3 times over ~10s before throwing. A signer refusal (CANCELLED) is NOT
  // retried — the user said no. This prevents a relay hiccup from cascading
  // into a 401 (when the JWT expires before signing recovers), which would
  // previously cause handleAuthError to wipe the session and redirect to /login.
  const pubkey = await withSignerRetry(() => signer.getPublicKey())

  const challengeRes = await fetch(`${API_BASE}/auth/challenge`)
  if (!challengeRes.ok) throw new Error(`challenge ${challengeRes.status}`)
  const { challenge, nonce } = (await challengeRes.json()) as ChallengeResponse

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
  if (!verifyEvent(signedEvent)) throw new Error('signature verification failed')

  const verifyRes = await fetch(`${API_BASE}/auth/verify`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ signedEvent }),
  })
  if (!verifyRes.ok) {
    const err = await verifyRes.json().catch(() => ({})) as { error?: string }
    throw new Error(err.error ?? `verify ${verifyRes.status}`)
  }
  const result = (await verifyRes.json()) as VerifyResponse

  const token = result.access_token || result.token
  if (!token) throw new Error('no token in verify response')

  localStorage.setItem('access_token', token)
  localStorage.setItem('token_expiry', result.expires_at)
  localStorage.setItem('user_pubkey', pubkey)
  localStorage.setItem('session_token', token)

  return pubkey
}

export function useActiveKeyReScope(): void {
  const { authState, signer } = useNostrAuth()
  const activePubkey = authState.activePubkey
  const queryClient = useQueryClient()

  // Track the pubkey we last successfully scoped the backend to
  const scopedPubkeyRef = useRef<string | null>(null)
  // Prevent concurrent re-auth attempts
  const inFlightRef = useRef(false)
  // True when this run is a first-time bootstrap, not a key switch.
  const bootstrappingRef = useRef(false)

  useEffect(() => {
    if (!activePubkey || !signer) return

    // FIRST RUN.
    //
    // This used to read user_pubkey and fall back to activePubkey when nothing
    // was stored:
    //
    //   scopedPubkeyRef.current = stored ?? activePubkey
    //
    // With nothing stored — a fresh browser, or a session whose token was
    // cleared — that recorded the CURRENT key, the equality check below matched
    // immediately, and the exchange never ran. No key "switch" ever follows on a
    // normal load, so the user sat on /login holding a perfectly valid shared
    // session. Measured against production: 2 of 4 loads landed on /login with
    // ZERO calls to /auth/challenge, while the signer returned 200 to
    // /api/v1/keys and /api/v1/nostrconnect/session on every single attempt.
    //
    // The guard's real purpose is narrower: don't re-auth when the backend is
    // ALREADY scoped to this key. That requires a usable token, not merely a
    // remembered pubkey.
    if (scopedPubkeyRef.current === null) {
      const stored = localStorage.getItem('user_pubkey')
      const token = localStorage.getItem('access_token')
      const expiry = localStorage.getItem('token_expiry')
      const tokenUsable =
        !!token && !!expiry && new Date(expiry) > new Date() && stored === activePubkey

      // Only claim we are scoped when there is a live token for this key.
      // Otherwise leave the ref null so the exchange below runs.
      scopedPubkeyRef.current = tokenUsable ? stored : null

      // Remember that this run is a first-time bootstrap rather than a key
      // switch. They need different endings: a switch just needs the cache
      // cleared, whereas a bootstrap has to get BackendAuthProvider to notice
      // the brand-new token, and it only reads it on mount.
      bootstrappingRef.current = !tokenUsable
    }

    // Already scoped to this key with a live token — nothing to do.
    // Reset the bootstrap counter: we are demonstrably working, so a later
    // legitimate bootstrap in this tab must not be blocked by a stale count.
    if (scopedPubkeyRef.current === activePubkey) {
      sessionStorage.removeItem('email_bootstrap_attempts')
      return
    }

    // Prevent concurrent attempts
    if (inFlightRef.current) return
    inFlightRef.current = true

    const targetPubkey = activePubkey

    reAuthForKey(signer)
      .then((newPubkey) => {
        scopedPubkeyRef.current = newPubkey

        if (bootstrappingRef.current) {
          bootstrappingRef.current = false

          // BOUNDED, because the navigation below is a HARD one and resets
          // every React ref — so a ref cannot detect a loop across it. If the
          // backend rejects the freshly minted token, BackendAuthProvider
          // clears it, we bootstrap again, and that repeats forever. Count the
          // attempts in sessionStorage (survives the reload, dies with the tab)
          // and stop after two, leaving the user on /login where the manual
          // sign-in still works.
          const KEY = 'email_bootstrap_attempts'
          const attempts = Number(sessionStorage.getItem(KEY) ?? '0') + 1
          sessionStorage.setItem(KEY, String(attempts))
          if (attempts > 2) {
            console.error(
              '[useActiveKeyReScope] bootstrap produced a token the backend will not accept ' +
                `(${attempts} attempts); staying on /login rather than looping`,
            )
            return
          }
          // BackendAuthProvider reads access_token on MOUNT, so simply storing
          // it leaves the app rendering !authed on /login with a valid session
          // sitting in localStorage. A hard navigation remounts the provider so
          // validateToken runs with a real Bearer token — the same ending
          // useLoginWithSigner uses after a manual sign-in.
          window.location.replace('/inbox')
          return
        }

        // Key switch: discard all mail data so the new identity's mailbox loads
        // immediately. No navigation — the user stays where they are.
        void queryClient.invalidateQueries()
      })
      .catch((err) => {
        console.error('[useActiveKeyReScope] re-auth failed for', targetPubkey, err)
        // Leave scopedPubkeyRef null so a later render retries. Without this a
        // transient backend blip during bootstrap would strand the user on
        // /login for the rest of the session.
        scopedPubkeyRef.current = null
      })
      .finally(() => {
        inFlightRef.current = false
      })
  }, [activePubkey, signer, queryClient])
}
