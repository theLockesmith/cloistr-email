import { Outlet, useNavigate } from 'react-router-dom'
import { Header, Footer, useBackendAuth } from '@cloistr/ui/components'
import Sidebar from './Sidebar'

export default function Layout() {
  const { isAuthenticated, user, logout } = useBackendAuth()
  const navigate = useNavigate()
  return (
    <div className="flex h-screen bg-cloistr-bg-elevated">
      <Sidebar />
      <div className="flex-1 flex flex-col">
        <Header
          activeServiceId="email"
          auth={{
            authenticated: isAuthenticated(),
            pubkey: user?.pubkey,
            // Provide onSignIn so the Header renders a Sign In button (navigates to
            // the login route) when signed out — without it the Header suppresses
            // the button for backend-auth apps.
            onSignIn: () => navigate('/login'),
            onLogout: () => {
              logout()
              navigate('/login')
            },
          }}
        />
        <main className="flex-1 overflow-auto">
          <Outlet />
        </main>
        <Footer />
      </div>
    </div>
  )
}
