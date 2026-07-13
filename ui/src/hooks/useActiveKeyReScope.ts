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
  const pubkey = await signer.getPublicKey()

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

  const signedEvent: NostrEvent = await signer.signEvent(unsignedEvent)
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

  useEffect(() => {
    if (!activePubkey || !signer) return

    // On first run, record the current pubkey from localStorage (set by login).
    // This prevents re-authing immediately on mount when already scoped correctly.
    if (scopedPubkeyRef.current === null) {
      const stored = localStorage.getItem('user_pubkey')
      scopedPubkeyRef.current = stored ?? activePubkey
    }

    // Already scoped to this key — nothing to do
    if (scopedPubkeyRef.current === activePubkey) return

    // Prevent concurrent attempts
    if (inFlightRef.current) return
    inFlightRef.current = true

    const targetPubkey = activePubkey

    reAuthForKey(signer)
      .then((newPubkey) => {
        scopedPubkeyRef.current = newPubkey
        // Flush all mail data so the new identity's mailbox loads immediately
        void queryClient.invalidateQueries()
      })
      .catch((err) => {
        console.error('[useActiveKeyReScope] re-auth failed for', targetPubkey, err)
        // Don't update scopedPubkeyRef — next activePubkey change will retry
      })
      .finally(() => {
        inFlightRef.current = false
      })
  }, [activePubkey, signer, queryClient])
}
