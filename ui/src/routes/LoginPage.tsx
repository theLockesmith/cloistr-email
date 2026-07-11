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
 */

import { useState } from 'react'
import { useNostrAuth } from '@cloistr/auth'
import { LoginModal } from '@cloistr/ui/components'
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

  const handleClose = async () => {
    setModalOpen(false)
    if (signer) {
      try {
        await loginWithSigner(signer)
        // loginWithSigner navigates on success
      } catch (err) {
        console.error('LoginPage: loginWithSigner failed', err)
      }
    }
  }

  return (
    <div className="min-h-screen flex flex-col items-center justify-center px-6 py-12 text-center">
      <div className="max-w-2xl">
        <h1 className="text-4xl font-bold mb-3">Cloistr Mail</h1>
        <p className="text-xl text-[var(--cloistr-text-muted)] mb-8">
          Encrypted, Nostr-native email. One address for email, Nostr, and Lightning.
        </p>

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
  )
}
