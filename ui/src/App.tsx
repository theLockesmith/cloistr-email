import { Routes, Route, Navigate } from 'react-router-dom'
import { useBackendAuth, useSharedSessionMaybe } from '@cloistr/ui/components'
import LoginPage from './routes/LoginPage'
import InboxPage from './routes/InboxPage'
import ComposePage from './routes/ComposePage'
import EmailPage from './routes/EmailPage'
import ContactsPage from './routes/ContactsPage'
import SettingsPage from './routes/SettingsPage'
import Layout from './components/Layout'
import { useSignerBunkerBootstrap } from './hooks/useSignerBunkerBootstrap'
import { useActiveKeyReScope } from './hooks/useActiveKeyReScope'
import { useRef } from 'react'

// ---------------------------------------------------------------------------
// Redirect-loop breaker constants
// ---------------------------------------------------------------------------
// If the app redirects to /login more than LOOP_MAX times within LOOP_WINDOW_MS,
// something is persistently wrong (two auth sources disagree, stale localStorage,
// etc.). Break the loop: clear all auth state and land on /login with an error
// message so the user can sign in manually rather than flickering forever.
const LOOP_MAX = 3
const LOOP_WINDOW_MS = 1_500

// Clear every localStorage key that auth code writes. Called when the loop
// breaker fires so the next manual login starts from a clean slate.
function clearAllAuthState() {
  const keys = ['access_token', 'token_expiry', 'user_pubkey', 'session_token']
  for (const k of keys) {
    localStorage.removeItem(k)
  }
}

function App() {
  const { isAuthenticated, loading } = useBackendAuth()
  const sharedSession = useSharedSessionMaybe()
  const authed = isAuthenticated()

  // Bootstrap signer↔mail NIP-46 connection for signer-session users.
  // Runs once after auth is confirmed; no-ops for bunker/NIP-07 users.
  useSignerBunkerBootstrap(authed)

  // Re-scope backend session and flush mailbox cache when the active key changes.
  useActiveKeyReScope()

  // isResolving stays true while BackendAuthProvider's SSO bootstrap is in
  // flight (bootstrapKeys + performBackendAuth). BackendAuthProvider.loading
  // only covers validateToken(), which completes long before SSO does. Without
  // this second gate the app renders !authed and immediately redirects to /login
  // before the silent SSO exchange finishes — the "flicker to /login" the
  // operator reported. BackendAuthProvider provides SharedSessionContext so
  // useSharedSessionMaybe() returns a live value from inside it.
  const isResolving = sharedSession?.isResolving ?? false

  // Per-render log of redirect-to-/login timestamps (module-level so it
  // survives React re-renders but resets on a full hard navigation).
  const loginRedirectLog = useRef<number[]>([])

  if (loading || isResolving) {
    return (
      <div className="flex items-center justify-center h-screen">
        <div className="text-lg">Loading...</div>
      </div>
    )
  }

  // Loop-breaker: count how many times in the last LOOP_WINDOW_MS we have
  // landed here with !authed. If it's >= LOOP_MAX, something is broken that
  // a redirect cannot fix. Clear all auth state and navigate to /login with
  // an error so the user gets a usable page instead of an infinite flicker.
  let loopBroken = false
  if (!authed) {
    const now = Date.now()
    const recent = loginRedirectLog.current.filter(t => now - t < LOOP_WINDOW_MS)
    recent.push(now)
    loginRedirectLog.current = recent
    if (recent.length >= LOOP_MAX) {
      clearAllAuthState()
      loopBroken = true
    }
  }

  return (
    <Routes>
      <Route
        path="/login"
        element={!authed ? <LoginPage /> : <Navigate to="/inbox" replace />}
      />

      {authed ? (
        <Route element={<Layout />}>
          <Route path="/inbox" element={<InboxPage />} />
          <Route path="/compose" element={<ComposePage />} />
          <Route path="/emails/:id" element={<EmailPage />} />
          <Route path="/contacts" element={<ContactsPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/" element={<Navigate to="/inbox" replace />} />
        </Route>
      ) : (
        <Route
          path="*"
          element={
            <Navigate
              to="/login"
              replace
              state={
                loopBroken
                  ? { authError: 'Your session could not be restored. Please sign in.' }
                  : undefined
              }
            />
          }
        />
      )}
    </Routes>
  )
}

export default App
