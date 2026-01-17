import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { emailAPI } from '../lib/api'

export default function InboxPage() {
  const { data: response, isLoading, error } = useQuery({
    queryKey: ['emails'],
    queryFn: () => emailAPI.list(),
  })

  const emails = response?.data?.data?.emails || []

  return (
    <div className="p-6">
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-3xl font-bold">Inbox</h1>
        <Link
          to="/compose"
          className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
        >
          Compose
        </Link>
      </div>

      {isLoading && <div className="text-center py-8">Loading emails...</div>}

      {error && (
        <div className="bg-red-100 border border-red-400 text-red-700 rounded p-4">
          Error loading emails
        </div>
      )}

      {emails.length === 0 && !isLoading && (
        <div className="text-center py-8 text-gray-600">
          No emails yet
        </div>
      )}

      <div className="bg-white rounded shadow">
        {emails.map((email: any) => (
          <Link
            key={email.id}
            to={`/emails/${email.id}`}
            className="block px-6 py-4 border-b hover:bg-gray-50 transition"
          >
            <div className="flex justify-between items-start">
              <div>
                <h3 className="font-semibold text-gray-900">{email.subject}</h3>
                <p className="text-sm text-gray-600">{email.from}</p>
              </div>
              <span className="text-sm text-gray-500">
                {new Date(email.created_at).toLocaleDateString()}
              </span>
            </div>
            <p className="text-sm text-gray-700 mt-1">{email.preview}</p>
          </Link>
        ))}
      </div>
    </div>
  )
}
