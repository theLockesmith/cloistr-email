/**
 * useRelayReconnect — Part 4 of the signer-resilience design.
 *
 * WHY THIS EXISTS
 *
 * When a phone backgrounds the page (app-switcher, file picker, screen lock)
 * the OS kills WebSocket connections. Parts 1-3 handle signing failures
 * gracefully after the fact. This hook prevents the failure from happening
 * at all by reconnecting relay sockets the moment the page becomes visible
 * again — before the user acts.
 *
 * Mail is the app where a spurious logout is most damaging (the operator
 * called this out specifically). The file picker, a quick screen-lock/unlock,
 * or switching away and back — all background the tab and kill the relay
 * sockets. Without this hook, the next signing attempt hits dead sockets and
 * the signer-error path fires before parts 1-3 can catch it gracefully.
 *
 * WHY DEBOUNCE
 *
 * A file-picker or a screen-lock/unlock sequence can fire visibilitychange
 * several times in rapid succession. Opening a relay connection on every flip
 * sends a burst of negotiation traffic. Full jitter in the retry policy (parts
 * 1-3) spreads individual app retries; debounce here collapses multiple rapid
 * events from the SAME tab into a single reconnect attempt.
 *
 * WHY REFS, NOT EFFECT DEPENDENCIES
 *
 * The event handlers are registered once (on mount, re-registered only if
 * debounceMs changes). Capturing authState or signer in the effect would cause
 * the listeners to be removed and re-added on every auth state change, creating
 * a window during rapid state transitions where no listener is attached. Refs
 * let the handlers always read the current value without being recreated.
 *
 * WHY NIP-46 ONLY
 *
 * NIP-07 (browser extension) signers do not hold persistent WebSockets that we
 * control — the extension manages its own connectivity. Only NIP-46 signers use
 * relay WebSockets that the OS can kill on backgrounding.
 *
 * WHAT "RECONNECT" MEANS
 *
 * The @cloistr/auth Nip46Signer lazy-connects: every operation checks its
 * internal isConnected flag and calls connect() if needed. Calling
 * getPublicKey() here is the lightest operation that exercises that path — it
 * warms the relay sockets up before the user needs them. The result is
 * discarded; failure is silently swallowed, because parts 1-3 handle it when
 * the user actually acts.
 *
 * SESSION STATE IS NEVER TOUCHED
 *
 * This module never calls logout, clearAuth, clearSharedSession, or any
 * session-clearing function — by construction. A reconnect hook that clears
 * auth reintroduces the exact bug the signer-resilience design exists to fix.
 *
 * NOTE: @cloistr/ui 0.27.0 will export this hook directly (as
 * useRelayReconnect). Until that version is published, mail carries its own
 * copy. When the upgrade lands, this file should be removed and the import in
 * App.tsx updated to '@cloistr/ui'.
 */

import { useEffect, useRef } from 'react'
import { useNostrAuth } from '@cloistr/auth'

export interface RelayReconnectOptions {
  /**
   * How long to wait after the last visibility or online event before
   * attempting to warm up the relay connection. Default: 300ms.
   */
  debounceMs?: number
}

/**
 * Reconnects relay WebSocket connections when the page regains visibility or
 * the network comes back online — before the user acts.
 *
 * Must be called inside a @cloistr/auth AuthProvider tree (BackendAuthProvider
 * satisfies this requirement).
 */
export function useRelayReconnect(options: RelayReconnectOptions = {}): void {
  const { debounceMs = 300 } = options
  const { authState, signer } = useNostrAuth()

  const authStateRef = useRef(authState)
  const signerRef = useRef(signer)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    authStateRef.current = authState
  }, [authState])

  useEffect(() => {
    signerRef.current = signer
  }, [signer])

  useEffect(() => {
    const scheduleReconnect = () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
      }

      timerRef.current = setTimeout(() => {
        timerRef.current = null
        const state = authStateRef.current
        const currentSigner = signerRef.current

        // Only act for NIP-46 sessions with a live signer. NIP-07 extensions
        // manage their own sockets. Sessions that were never established have
        // nothing to warm up.
        if (!state.isConnected || state.method !== 'nip46' || currentSigner === null) {
          return
        }

        // getPublicKey() exercises the Nip46Signer's lazy-connect path: it
        // calls connect() internally when WebSockets are down. We discard the
        // result — this is a warm-up, not a verification. Failure is
        // intentionally swallowed here; SignerRecovery (part 3) handles it if
        // the user then takes an action that needs signing.
        currentSigner.getPublicKey().catch(() => {
          // Reconnect attempt failed. The user will encounter the error on
          // their next signed action, at which point parts 1-3 apply.
        })
      }, debounceMs)
    }

    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        scheduleReconnect()
      }
    }

    const onOnline = () => {
      scheduleReconnect()
    }

    document.addEventListener('visibilitychange', onVisibilityChange)
    window.addEventListener('online', onOnline)

    return () => {
      document.removeEventListener('visibilitychange', onVisibilityChange)
      window.removeEventListener('online', onOnline)
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
    }
  }, [debounceMs])
}
