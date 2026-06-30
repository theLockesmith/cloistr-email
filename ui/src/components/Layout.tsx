import { Outlet, useNavigate } from 'react-router-dom'
import { Header, Footer, useBackendAuth } from '@cloistr/ui/components'
import Sidebar from './Sidebar'

export default function Layout() {
  const { isAuthenticated, user, logout } = useBackendAuth()
  const navigate = useNavigate()
  return (
    <div className="flex h-screen bg-gray-100">
      <Sidebar />
      <div className="flex-1 flex flex-col">
        <Header
          activeServiceId="email"
          auth={{
            authenticated: isAuthenticated(),
            pubkey: user?.pubkey,
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
