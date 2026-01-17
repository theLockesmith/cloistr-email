import { Link } from 'react-router-dom'

export default function Sidebar() {
  return (
    <aside className="w-64 bg-white shadow">
      <div className="p-6">
        <h1 className="text-2xl font-bold text-gray-900">Coldforge Mail</h1>
      </div>

      <nav className="px-6 py-4">
        <ul className="space-y-2">
          <li>
            <Link
              to="/inbox"
              className="px-4 py-2 rounded hover:bg-gray-100 block"
            >
              Inbox
            </Link>
          </li>
          <li>
            <Link
              to="/compose"
              className="px-4 py-2 rounded hover:bg-gray-100 block"
            >
              Compose
            </Link>
          </li>
          <li>
            <Link
              to="/contacts"
              className="px-4 py-2 rounded hover:bg-gray-100 block"
            >
              Contacts
            </Link>
          </li>
          <li>
            <Link
              to="/settings"
              className="px-4 py-2 rounded hover:bg-gray-100 block"
            >
              Settings
            </Link>
          </li>
        </ul>
      </nav>
    </aside>
  )
}
