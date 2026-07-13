import { Routes, Route, Navigate } from 'react-router-dom'
import { useBackendAuth } from '@cloistr/ui/components'
import LoginPage from './routes/LoginPage'
import InboxPage from './routes/InboxPage'
import ComposePage from './routes/ComposePage'
import EmailPage from './routes/EmailPage'
import ContactsPage from './routes/ContactsPage'
import SettingsPage from './routes/SettingsPage'
import Layout from './components/Layout'
import { useSignerBunkerBootstrap } from './hooks/useSignerBunkerBootstrap'
import { useActiveKeyReScope } from './hooks/useActiveKeyReScope'

function App() {
  const { isAuthenticated, loading } = useBackendAuth()
  const authed = isAuthenticated()

  // Bootstrap signer↔mail NIP-46 connection for signer-session users.
  // Runs once after auth is confirmed; no-ops for bunker/NIP-07 users.
  useSignerBunkerBootstrap(authed)

  // Re-scope backend session and flush mailbox cache when the active key changes.
  useActiveKeyReScope()

  if (loading) {
    return (
      <div className="flex items-center justify-center h-screen">
        <div className="text-lg">Loading...</div>
      </div>
    )
  }

  return (
    <Routes>
      <Route
        path="/login"
        element={!authed ? <LoginPage /> : <Navigate to="/inbox" />}
      />

      {authed ? (
        <Route element={<Layout />}>
          <Route path="/inbox" element={<InboxPage />} />
          <Route path="/compose" element={<ComposePage />} />
          <Route path="/emails/:id" element={<EmailPage />} />
          <Route path="/contacts" element={<ContactsPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/" element={<Navigate to="/inbox" />} />
        </Route>
      ) : (
        <Route path="*" element={<Navigate to="/login" />} />
      )}
    </Routes>
  )
}

export default App
