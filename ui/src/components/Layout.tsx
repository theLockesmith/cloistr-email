import { useEffect, useState } from 'react'
import { Outlet, useNavigate } from 'react-router-dom'
import { Header, Footer, useBackendAuth } from '@cloistr/ui/components'
import Sidebar from './Sidebar'

export default function Layout() {
  const { isAuthenticated, user, logout } = useBackendAuth()
  const navigate = useNavigate()
  // Drawer state. Only meaningful below `md`; from `md` up the sidebar is a
  // static column and this is ignored.
  const [navOpen, setNavOpen] = useState(false)

  // Escape closes the drawer. Without this the only ways out are the backdrop
  // and the close button, which is poor for keyboard and screen-reader users.
  useEffect(() => {
    if (!navOpen) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setNavOpen(false)
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [navOpen])

  return (
    <div className="flex h-screen bg-cloistr-bg-elevated">
      <Sidebar isOpen={navOpen} onClose={() => setNavOpen(false)} />
      {/* min-w-0 so the content column can actually shrink inside the flex row.
          Without it a wide child (the message list, a long subject) sets the
          floor and the layout overflows horizontally on a phone. */}
      <div className="flex-1 flex flex-col min-w-0">
        <div className="flex items-center">
          {/* Hamburger: mobile only. The sidebar is off-canvas below `md`, so
              this is the only way to reach navigation there. */}
          <button
            type="button"
            className="md:hidden px-4 py-3 text-2xl leading-none text-cloistr-text"
            aria-label="Open navigation"
            aria-expanded={navOpen}
            onClick={() => setNavOpen(true)}
          >
            &#9776;
          </button>
          <div className="flex-1 min-w-0">
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
          </div>
        </div>
        <main className="flex-1 overflow-auto">
          <Outlet />
        </main>
        <Footer />
      </div>
    </div>
  )
}
