/**
 * LoginPage
 *
 * Renders the shared @cloistr/ui LoginModal (connect mode) in place of the
 * previous bespoke NIP-07 / NIP-46 UI. Username/password ("Sign in with
 * Cloistr") is the primary method; NIP-07 extension, NIP-46 bunker, passkey,
 * and Lightning are available under "Other login methods".
 *
 * Auth bridge
 * -----------
 * BackendAuthProvider (wrapping AuthProvider internally) already provides the
 * @cloistr/auth context that LoginModal needs — no extra AuthProvider wrapper
 * in main.tsx is required.
 *
 * When LoginModal completes (any method), @cloistr/auth's authState.signer is
 * set. handleClose reads it and calls loginWithSigner (useLoginWithSigner),
 * which runs email's backend challenge/verify with the signer to obtain a JWT,
 * stores it in localStorage, then navigates to /inbox so BackendAuthProvider
 * re-mounts, calls validateToken with a real Bearer token, and
 * isAuthenticated() flips to true.
 *
 * For the signer-session cookie path (password login sets .cloistr.xyz
 * auth_token cookie): email's AuthMiddleware also accepts that cookie, so API
 * calls will succeed even before the nostrconnect signer resolves. The
 * nostrconnect signer resolved by LoginModal is needed for the JWT path and
 * for the useSignerBunkerBootstrap encryption hook.
 */

import { useNostrAuth } from '@cloistr/auth'
import { LoginModal } from '@cloistr/ui/components'
import { useLoginWithSigner } from '../hooks/useLoginWithSigner'

const SIGNER_URL = 'https://signer.cloistr.xyz'

export default function LoginPage() {
  const { signer } = useNostrAuth()
  const { loginWithSigner } = useLoginWithSigner()

  // Called when LoginModal closes (success or cancel).
  // On success, signer (from useNostrAuth) is the fully connected SignerInterface;
  // hand it to loginWithSigner to bridge to email's JWT session.
  const handleClose = async () => {
    if (signer) {
      try {
        await loginWithSigner(signer)
        // loginWithSigner does window.location.href = '/inbox' on success
      } catch (err) {
        // Error surfaced; modal stays open (LoginModal is always-open here)
        console.error('LoginPage: loginWithSigner failed', err)
      }
    }
    // If signer is null (user clicked Cancel), do nothing — modal stays open
  }

  return (
    <LoginModal
      isOpen={true}
      onClose={handleClose}
      signerUrl={SIGNER_URL}
    />
  )
}
