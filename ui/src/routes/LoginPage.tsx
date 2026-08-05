/**
 * LoginPage — purpose landing + login CTA for mail.cloistr.xyz
 *
 * Unauthenticated users land here. Rather than an opaque login wall, we show a
 * short purpose-conveying hero (normie adoption: a cold visitor should
 * understand what the page is) with a "Sign in with Cloistr" button that opens
 * the shared LoginModal. Signer-session users never reach this page —
 * BackendAuthProvider's SSO probe auto-authenticates them first.
 *
 * Auth bridge: when LoginModal completes, @cloistr/auth's signer is set;
 * handleClose hands it to loginWithSigner (email backend challenge/verify → JWT
 * → navigates to the intended route, defaulting to /inbox).
 *
 * Error state: the App.tsx loop breaker navigates here with
 * location.state.authError when it detects rapid oscillation between /login and
 * a protected route. This surfaces a human-readable message instead of leaving
 * the user watching an infinite flicker.
 */

import { useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useNostrAuth } from '@cloistr/auth'
import { Header, LoginModal } from '@cloistr/ui/components'
import { useLoginWithSigner } from '../hooks/useLoginWithSigner'

const SIGNER_URL = 'https://signer.cloistr.xyz'

const FEATURES = [
  { icon: '🔑', title: 'Your keys, your inbox', body: 'Your Nostr identity is your login. No passwords to leak, no account to lose.' },
  { icon: '🔒', title: 'Encrypted by default', body: 'Messages between Cloistr users are end-to-end encrypted — the server never sees plaintext.' },
  { icon: '✉️', title: 'Real email, too', body: 'Send and receive with the rest of the world over SMTP. One address for Nostr, email, and Lightning.' },
]

export default function LoginPage() {
  const { signer } = useNostrAuth()
  const { loginWithSigner } = useLoginWithSigner()
  const [modalOpen, setModalOpen] = useState(false)
  const [loginError, setLoginError] = useState<string | null>(null)

  // The App.tsx loop breaker passes an authError in location state when it
  // detects repeated /login ↔ protected-route oscillation. Show it here so the
  // user knows something went wrong and can take action (sign in manually).
  const location = useLocation()
  const locationError = (location.state as { authError?: string } | null)?.authError ?? null

  const handleClose = async () => {
    setModalOpen(false)
    setLoginError(null)
    if (signer) {
      try {
        await loginWithSigner(signer)
        // loginWithSigner navigates on success
      } catch (err) {
        const msg = err instanceof Error ? err.message : 'Sign-in failed'
        setLoginError(msg)
        console.error('LoginPage: loginWithSigner failed', err)
      }
    }
  }

  // Prefer a fresh login-attempt error over the stale loop-breaker message.
  const displayError = loginError ?? locationError

  return (
    <>
      <Header activeServiceId="email" auth={{ authenticated: false }} signerUrl="https://signer.cloistr.xyz" />
    <div className="min-h-screen flex flex-col items-center justify-center px-6 py-12 text-center">
      <div className="max-w-2xl">
        <h1 className="text-4xl font-bold mb-3">Cloistr Mail</h1>
        <p className="text-xl text-[var(--cloistr-text-muted)] mb-8">
          Encrypted, Nostr-native email. One address for email, Nostr, and Lightning.
        </p>

        {displayError && (
          <div className="mb-6 rounded-lg border border-[var(--cloistr-error,#f87171)]/40 bg-[var(--cloistr-error,#f87171)]/10 px-4 py-3 text-sm text-[var(--cloistr-error,#dc2626)]">
            {displayError}
          </div>
        )}

        <button
          onClick={() => setModalOpen(true)}
          className="inline-flex items-center gap-2 rounded-lg bg-[var(--cloistr-primary)] px-6 py-3 text-white font-medium hover:bg-[var(--cloistr-primary-hover)] transition-colors"
        >
          Sign in with Cloistr
        </button>
        <p className="mt-3 text-sm text-[var(--cloistr-text-muted)]">
          New here? Get a Nostr identity at{' '}
          <a href={SIGNER_URL} className="text-[var(--cloistr-primary)] hover:underline">
            signer.cloistr.xyz
          </a>
        </p>

        <div className="mt-12 grid gap-6 sm:grid-cols-3 text-left">
          {FEATURES.map((f) => (
            <div key={f.title} className="rounded-lg border border-[var(--cloistr-border)] p-4">
              <div className="text-2xl mb-2">{f.icon}</div>
              <div className="font-semibold mb-1">{f.title}</div>
              <div className="text-sm text-[var(--cloistr-text-muted)]">{f.body}</div>
            </div>
          ))}
        </div>
      </div>

      <LoginModal isOpen={modalOpen} onClose={handleClose} signerUrl={SIGNER_URL} />
    </div>
    </>
  )
}
