import { useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { AppShell, AppShellToggle, Header, Footer, useBackendAuth } from '@cloistr/ui/components'

/**
 * Key for the desktop icons-only preference.
 *
 * Persisted because a collapse the app forgets on every navigation is worse
 * than no collapse at all. The shared Sidebar deliberately does not own this.
 */
const COLLAPSE_KEY = 'cloistr-mail-sidebar-collapsed'

function readCollapsed(): boolean {
  try {
    return localStorage.getItem(COLLAPSE_KEY) === '1'
  } catch {
    return false
  }
}

const NAV_ITEMS = [
  { id: 'inbox', label: 'Inbox', href: '/inbox', icon: '📥' },
  { id: 'compose', label: 'Compose', href: '/compose', icon: '✍️' },
  { id: 'contacts', label: 'Contacts', href: '/contacts', icon: '👥' },
  { id: 'filters', label: 'Filters', href: '/filters', icon: '🔍' },
  { id: 'settings', label: 'Settings', href: '/settings', icon: '⚙️' },
] as const

type NavId = (typeof NAV_ITEMS)[number]['id']

/**
 * MailNav — navigation list for AppShell's `nav` prop.
 *
 * WHY NOT <Sidebar> DIRECTLY
 *
 * The shared Sidebar adds `position: fixed` at < 768px (its own mobile-drawer
 * CSS). AppShell also renders a `position: fixed` drawer. A fixed child inside
 * a fixed parent escapes the parent's layout context and sits at the viewport
 * edge — the Sidebar ends up hidden behind the shell's drawer rather than
 * inside it.
 *
 * This component renders the same items and CSS classes for visual
 * consistency, but carries no mobile-drawer wrapper. AppShell owns the
 * single mobile affordance; MailNav is just the content it places inside.
 *
 * Desktop collapse (icons-only rail) is preserved via `.mail-nav--collapsed`
 * and the companion CSS in index.css.
 */
function MailNav({
  activeId,
  collapsed,
  onCollapsedChange,
}: {
  activeId?: NavId
  collapsed: boolean
  onCollapsedChange: (next: boolean) => void
}) {
  return (
    <div
      className={`mail-nav${collapsed ? ' mail-nav--collapsed' : ''}`}
      aria-label="Mail navigation"
    >
      {/* App branding */}
      <div className="mail-nav-header">
        {collapsed ? (
          <img src="/cloistr-icon.svg" alt="Cloistr Mail" className="w-8 h-8" />
        ) : (
          <a href="/inbox" className="mail-nav-wordmark">
            <img src="/cloistr-icon.svg" alt="" className="w-8 h-8" />
            <span className="text-xl font-bold text-cloistr-text">Cloistr Mail</span>
          </a>
        )}
      </div>

      {/* Navigation items */}
      <nav>
        <ul className="mail-nav-list">
          {NAV_ITEMS.map((item) => {
            const isActive = item.id === activeId
            return (
              <li key={item.id}>
                <a
                  href={item.href}
                  className={`cloistr-sidebar-item${isActive ? ' cloistr-sidebar-item--active' : ''}`}
                  aria-current={isActive ? 'page' : undefined}
                  /* When collapsed the label is hidden; icon needs an
                     accessible name and a tooltip. */
                  title={collapsed ? item.label : undefined}
                  aria-label={collapsed ? item.label : undefined}
                >
                  <span className="cloistr-sidebar-icon">{item.icon}</span>
                  <span className="cloistr-sidebar-label">{item.label}</span>
                </a>
              </li>
            )
          })}
        </ul>
      </nav>

      {/* Desktop collapse toggle — mirrors Sidebar's own control so the
          affordance is unchanged for users who relied on it. */}
      <button
        type="button"
        className="cloistr-sidebar-collapse"
        aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
        aria-expanded={!collapsed}
        onClick={() => onCollapsedChange(!collapsed)}
      >
        <svg
          width="20"
          height="20"
          viewBox="0 0 24 24"
          fill="none"
          aria-hidden="true"
        >
          <path
            d={collapsed ? 'M9 18l6-6-6-6' : 'M15 18l-6-6 6-6'}
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
        <span className="cloistr-sidebar-label">Collapse</span>
      </button>
    </div>
  )
}

/*
 * Mail has NO app menu bar, deliberately.
 *
 * The AppShell migration invented a File/Go menu for this app. It never had
 * one, and every item in it — Compose, Inbox, Contacts, Filters, Settings —
 * already exists in NAV_ITEMS, i.e. in the sidebar three inches to the left.
 * That put a second, redundant navigation surface on desktop and cost a row of
 * vertical space for nothing.
 *
 * Operator: "mail now has a menu bar and it's not supposed to, only the
 * sidebar."
 *
 * Mail declares `nav` and no `menu`. AppShell then renders the rail on desktop
 * with a collapse toggle, and one drawer on mobile — which is the whole point
 * of the shell deciding presentation from what the app declares.
 */

export default function Layout() {
  const { isAuthenticated, user, logout } = useBackendAuth()
  const navigate = useNavigate()
  const location = useLocation()

  // Desktop icons-only rail. Ignored below the AppShell breakpoint (768px).
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
  const activeId = NAV_ITEMS.reduce<NavId | undefined>((best, item) => {
    if (!location.pathname.startsWith(item.href)) return best
    const current = NAV_ITEMS.find((i) => i.id === best)
    return item.href.length > (current?.href?.length ?? 0) ? item.id : best
  }, undefined)

  
  return (
    <AppShell
      serviceId="email"
      nav={
        <MailNav
          activeId={activeId}
          collapsed={collapsed}
          onCollapsedChange={setCollapsedPersisted}
        />
      }
      toggleInHeader
    >
      {/* The ONE nav control, portaled into the shared Header. */}
      <AppShellToggle />

      {/*
       * Children render inside `cloistr-appshell-content` (overflow:auto;
       * flex:1). The wrapper div:
       *   - h-full fills the content area so Header, scroll zone, and Footer
       *     are always distributed correctly;
       *   - flex flex-col stacks them vertically;
       *   - min-h-0 prevents a flex-item min-height floor from leaking out.
       *
       * The inner scroll div (flex-1 overflow-auto min-h-0) is the single
       * scroll container for all page content. Pages such as InboxPage that
       * declare h-full on their outer element get exactly the available space
       * between Header and Footer, and handle their own interior scrolling.
       */}
      <div className="flex flex-col h-full min-h-0">
        <Header
          activeServiceId="email"
          auth={{
            authenticated: isAuthenticated(),
            pubkey: user?.pubkey,
            // onSignIn makes the Header render a Sign In button when signed
            // out. Without it the Header suppresses the button for backend-
            // auth apps, so unauthenticated users would have no visible prompt.
            onSignIn: () => navigate('/login'),
            onLogout: () => {
              logout()
              navigate('/login')
            },
          }}
        />
        <div className="flex-1 overflow-auto min-h-0">
          <Outlet />
        </div>
        <Footer />
      </div>
    </AppShell>
  )
}
