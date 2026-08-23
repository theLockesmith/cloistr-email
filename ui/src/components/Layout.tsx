import { useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { Header, Footer, Sidebar, SidebarToggle, useBackendAuth } from '@cloistr/ui/components'
import type { SidebarItem } from '@cloistr/ui/components'

/**
 * Key for the desktop icons-only preference.
 *
 * Persisted because a collapse the app forgets on every navigation is worse
 * than no collapse at all — you set it, move to another page, and it is back.
 * The shared Sidebar deliberately does not own this: a component that reached
 * into storage on the app's behalf would be guessing at the app's key.
 */
const COLLAPSE_KEY = 'cloistr-mail-sidebar-collapsed'

function readCollapsed(): boolean {
  try {
    return localStorage.getItem(COLLAPSE_KEY) === '1'
  } catch {
    // Private-mode Safari throws on localStorage access. A missing preference
    // is not worth breaking navigation over.
    return false
  }
}

const NAV: SidebarItem[] = [
  { id: 'inbox', label: 'Inbox', href: '/inbox', icon: '📥' },
  { id: 'compose', label: 'Compose', href: '/compose', icon: '✍️' },
  { id: 'contacts', label: 'Contacts', href: '/contacts', icon: '👥' },
  { id: 'filters', label: 'Filters', href: '/filters', icon: '🔍' },
  { id: 'settings', label: 'Settings', href: '/settings', icon: '⚙️' },
]

export default function Layout() {
  const { isAuthenticated, user, logout } = useBackendAuth()
  const navigate = useNavigate()
  const location = useLocation()

  // Mobile drawer visibility. Ignored at `md` and up.
  const [navOpen, setNavOpen] = useState(false)
  // Desktop icons-only rail. Ignored below `md`.
  const [collapsed, setCollapsed] = useState(readCollapsed)

  const setCollapsedPersisted = (next: boolean) => {
    setCollapsed(next)
    try {
      localStorage.setItem(COLLAPSE_KEY, next ? '1' : '0')
    } catch {
      // Preference is a nicety; never let it break the toggle.
    }
  }

  // Match the longest nav prefix so /inbox/<id> still highlights Inbox.
  const activeId = NAV.reduce<string | undefined>((best, item) => {
    if (!item.href || !location.pathname.startsWith(item.href)) return best
    const current = NAV.find(i => i.id === best)
    return item.href.length > (current?.href?.length ?? 0) ? item.id : best
  }, undefined)

  return (
    <div className="flex bg-cloistr-bg-elevated" style={{height:"100dvh"}}>
      {/* Shared Sidebar, replacing mail's hand-rolled one.
          Mail's version had the mobile drawer but NO desktop collapse, so the
          icons-only rail simply did not exist here. Every app rolling its own
          is how they diverged: different glyphs, different z-index layers, four
          apps independently painting the header over their own open drawer.
          Escape-to-close now lives in the component, so the local effect that
          did it here is gone. */}
      <Sidebar
        items={NAV}
        activeId={activeId}
        open={navOpen}
        onOpenChange={setNavOpen}
        collapsed={collapsed}
        onCollapsedChange={setCollapsedPersisted}
        ariaLabel="Mail navigation"
        header={
          <a href="/inbox" className="flex items-center gap-3">
            <img src="/cloistr-icon.svg" alt="" className="w-8 h-8" />
            <span className="text-xl font-bold text-cloistr-text">Cloistr Mail</span>
          </a>
        }
        collapsedHeader={<img src="/cloistr-icon.svg" alt="Cloistr Mail" className="w-8 h-8" />}
      />

      {/* min-w-0 so the content column can actually shrink inside the flex row.
          Without it a wide child (the message list, a long subject) sets the
          floor and the layout overflows horizontally on a phone. */}
      <div className="flex-1 flex flex-col min-w-0">
        <div className="flex items-center">
          {/* Shared toggle so mail opens its drawer with the same glyph as every
              other app — mail used &#9776; and stash used its own button. */}
          <SidebarToggle onClick={() => setNavOpen(true)} expanded={navOpen} />
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
