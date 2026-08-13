import { Link } from 'react-router-dom'

const NAV = [
  { to: '/inbox', label: 'Inbox' },
  { to: '/compose', label: 'Compose' },
  { to: '/contacts', label: 'Contacts' },
  { to: '/settings', label: 'Settings' },
] as const

interface SidebarProps {
  /** Whether the drawer is open. Only affects the mobile (< md) presentation. */
  isOpen: boolean
  /** Close the drawer. Called on backdrop click, Escape, and nav selection. */
  onClose: () => void
}

/**
 * Navigation sidebar.
 *
 * COLLAPSIBLE BELOW `md`. It used to be a permanently-mounted `w-64` column,
 * which on a 375px phone consumed ~68% of the viewport — compose was unusable
 * with barely any room left for the message body.
 *
 * Below `md` it is an off-canvas drawer: translated out of view, slid in when
 * open, over a dismissable backdrop. From `md` up it is the static column it
 * always was, and the toggle/backdrop are not rendered at all.
 */
export default function Sidebar({ isOpen, onClose }: SidebarProps) {
  return (
    <>
      {/* Backdrop: mobile only, and only while open. Closes on tap. */}
      {isOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/50 md:hidden"
          aria-hidden="true"
          onClick={onClose}
        />
      )}

      <aside
        className={[
          'w-64 bg-cloistr-bg shadow',
          // Mobile: off-canvas drawer.
          'fixed inset-y-0 left-0 z-40 transform transition-transform duration-200 ease-out',
          isOpen ? 'translate-x-0' : '-translate-x-full',
          // md+: back to a static in-flow column, always visible.
          'md:static md:z-auto md:translate-x-0 md:transform-none md:shrink-0',
        ].join(' ')}
        aria-label="Mail navigation"
      >
        <div className="flex items-center justify-between p-6">
          <Link to="/inbox" className="flex items-center gap-3" onClick={onClose}>
            <img src="/cloistr-icon.svg" alt="Cloistr" className="w-8 h-8" />
            <h1 className="text-2xl font-bold text-cloistr-text">Cloistr Mail</h1>
          </Link>
          {/* Close control, mobile only — the drawer overlays the content, so it
              needs a way out that does not depend on hitting the backdrop. */}
          <button
            type="button"
            className="md:hidden text-cloistr-text-muted hover:text-cloistr-text text-2xl leading-none px-2"
            aria-label="Close navigation"
            onClick={onClose}
          >
            &times;
          </button>
        </div>

        <nav className="px-6 py-4">
          <ul className="space-y-2">
            {NAV.map((item) => (
              <li key={item.to}>
                <Link
                  to={item.to}
                  className="px-4 py-2 rounded hover:bg-cloistr-bg-hover block"
                  // Selecting a destination on mobile should also dismiss the
                  // drawer, otherwise it stays over the page just navigated to.
                  onClick={onClose}
                >
                  {item.label}
                </Link>
              </li>
            ))}
          </ul>
        </nav>
      </aside>
    </>
  )
}
