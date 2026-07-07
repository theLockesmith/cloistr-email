import { useEffect, useRef } from 'react'
import { isCloistrDomain } from '@cloistr/ui'
import { apiV2 } from '../lib/api'

const SIGNER_BASE = 'https://signer.cloistr.xyz'
const POLL_INTERVAL_MS = 1500
const POLL_TIMEOUT_MS = 60_000

interface Nip46StatusResponse {
  connected: boolean
}

interface NostrConnectInitResponse {
  nostrconnect_uri: string
  nonce: string
}

interface SignerKeysResponse {
  keys: Array<{ id: string; [key: string]: unknown }>
}

interface SignerSessionResponse {
  success?: boolean
  consent_required?: boolean
}

/**
 * useSignerBunkerBootstrap
 *
 * After the user is confirmed authenticated, checks whether the mail backend
 * already has a live NIP-46 bunker connection. If not, and the user is on a
 * cloistr.xyz domain (i.e. a signer-session user), initiates the nostrconnect
 * handshake automatically:
 *
 *   1. GET /api/v2/auth/nip46/status — if connected, done.
 *   2. GET signer /api/v1/keys — get keyId. If no keys, bail (not a
 *      signer-session user; bunker/NIP-07 users have no signer keys).
 *   3. POST /api/v2/auth/nostrconnect/init — get URI + nonce.
 *   4. POST signer /api/v1/nostrconnect/session — present URI + consent.
 *   5. Poll /api/v2/auth/nip46/status every 1.5s (max 60s) until connected.
 *
 * The hook is a fire-and-forget effect: it never blocks render and never
 * throws to the caller.
 */
export function useSignerBunkerBootstrap(isAuthenticated: boolean): void {
  // Guard: only run once per mount while authenticated
  const ranRef = useRef(false)

  useEffect(() => {
    if (!isAuthenticated) return
    if (ranRef.current) return

    // Only attempt on cloistr.xyz (signer-session users); skip on localhost dev
    // or bunker/NIP-07 users accessing from non-cloistr domains.
    if (!isCloistrDomain()) return

    ranRef.current = true

    let pollTimer: ReturnType<typeof setInterval> | null = null
    let aborted = false

    const stopPolling = () => {
      if (pollTimer !== null) {
        clearInterval(pollTimer)
        pollTimer = null
      }
    }

    const run = async (): Promise<void> => {
      // Step 1: already connected?
      try {
        const statusRes = await apiV2.get<Nip46StatusResponse>('/auth/nip46/status')
        if (statusRes.data.connected) return
      } catch {
        // Network error — bail quietly; don't disrupt normal app usage
        return
      }

      if (aborted) return

      // Step 2: fetch signer key id (proves this is a signer-session user)
      let keyId: string
      try {
        const keysRes = await fetch(`${SIGNER_BASE}/api/v1/keys`, {
          credentials: 'include',
        })
        if (!keysRes.ok) return
        const keysData = (await keysRes.json()) as SignerKeysResponse
        if (!keysData.keys || keysData.keys.length === 0) {
          // Not a signer-session user (pure bunker/NIP-07) — bail silently
          return
        }
        keyId = keysData.keys[0].id
      } catch {
        return
      }

      if (aborted) return

      // Step 3: get nostrconnect URI from mail backend
      let nostrconnectUri: string
      try {
        const initRes = await apiV2.post<NostrConnectInitResponse>(
          '/auth/nostrconnect/init',
          {},
        )
        nostrconnectUri = initRes.data.nostrconnect_uri
      } catch {
        return
      }

      if (aborted) return

      // Step 4: present URI to signer (signer does the NIP-46 handshake)
      try {
        const sessionRes = await fetch(
          `${SIGNER_BASE}/api/v1/nostrconnect/session`,
          {
            method: 'POST',
            credentials: 'include',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              uri: nostrconnectUri,
              key_id: keyId,
              consent: true,
            }),
          },
        )
        const sessionData = (await sessionRes.json()) as SignerSessionResponse
        // consent_required on first run is expected — signer records consent and
        // proceeds; the NIP-46 handshake continues asynchronously on the signer.
        if (!sessionRes.ok && !sessionData.consent_required) {
          return
        }
      } catch {
        return
      }

      if (aborted) return

      // Step 5: poll until backend confirms connected
      const pollStart = Date.now()
      pollTimer = setInterval(() => {
        if (aborted) {
          stopPolling()
          return
        }
        if (Date.now() - pollStart > POLL_TIMEOUT_MS) {
          // Timeout: stop quietly. Mail still works for new client-side mail;
          // old server-side mail will show the "Connecting your signer…" note.
          stopPolling()
          return
        }
        void apiV2
          .get<Nip46StatusResponse>('/auth/nip46/status')
          .then((s) => {
            if (s.data.connected) stopPolling()
          })
          .catch(() => {
            // transient error — keep polling until timeout
          })
      }, POLL_INTERVAL_MS)
    }

    void run()

    return () => {
      aborted = true
      stopPolling()
    }
  }, [isAuthenticated])
}
