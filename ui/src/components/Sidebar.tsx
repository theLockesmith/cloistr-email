import { Link } from 'react-router-dom'

export default function Sidebar() {
  return (
    <aside className="w-64 bg-cloistr-bg shadow">
      <div className="p-6">
        <Link to="/inbox" className="flex items-center gap-3">
          <img src="/cloistr-icon.svg" alt="Cloistr" className="w-8 h-8" />
          <h1 className="text-2xl font-bold text-cloistr-text">Cloistr Mail</h1>
        </Link>
      </div>

      <nav className="px-6 py-4">
        <ul className="space-y-2">
          <li>
            <Link
              to="/inbox"
              className="px-4 py-2 rounded hover:bg-cloistr-bg-hover block"
            >
              Inbox
            </Link>
          </li>
          <li>
            <Link
              to="/compose"
              className="px-4 py-2 rounded hover:bg-cloistr-bg-hover block"
            >
              Compose
            </Link>
          </li>
          <li>
            <Link
              to="/contacts"
              className="px-4 py-2 rounded hover:bg-cloistr-bg-hover block"
            >
              Contacts
            </Link>
          </li>
          <li>
            <Link
              to="/settings"
              className="px-4 py-2 rounded hover:bg-cloistr-bg-hover block"
            >
              Settings
            </Link>
          </li>
        </ul>
      </nav>
    </aside>
  )
}
