import { useParams, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { emailAPI } from '../lib/api'

export default function EmailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()

  const { data: response, isLoading, error } = useQuery({
    queryKey: ['email', id],
    queryFn: () => (id ? emailAPI.get(id) : Promise.reject('No ID')),
  })

  const email = response?.data?.data

  if (isLoading) {
    return <div className="p-6">Loading email...</div>
  }

  if (error || !email) {
    return (
      <div className="p-6">
        <div className="bg-red-100 border border-red-400 text-red-700 rounded p-4">
          Email not found
        </div>
        <button
          onClick={() => navigate('/inbox')}
          className="mt-4 px-4 py-2 text-gray-700 border border-gray-300 rounded hover:bg-gray-50"
        >
          Back to Inbox
        </button>
      </div>
    )
  }

  return (
    <div className="p-6">
      <button
        onClick={() => navigate('/inbox')}
        className="mb-6 px-4 py-2 text-gray-700 border border-gray-300 rounded hover:bg-gray-50"
      >
        Back to Inbox
      </button>

      <div className="bg-white rounded shadow p-6">
        <h1 className="text-3xl font-bold mb-4">{email.subject}</h1>

        <div className="border-b pb-4 mb-4">
          <p className="text-sm text-gray-600">
            From: <span className="font-semibold">{email.from}</span>
          </p>
          <p className="text-sm text-gray-600">
            To: <span className="font-semibold">{email.to}</span>
          </p>
          <p className="text-sm text-gray-600">
            {new Date(email.created_at).toLocaleString()}
          </p>

          {email.is_encrypted && (
            <div className="mt-2 inline-block px-3 py-1 bg-green-100 text-green-800 text-sm rounded">
              Encrypted
            </div>
          )}
        </div>

        <div className="prose max-w-none mb-6">
          {email.body}
        </div>

        {email.attachments && email.attachments.length > 0 && (
          <div className="border-t pt-4">
            <h3 className="font-semibold mb-2">Attachments</h3>
            <ul className="space-y-2">
              {email.attachments.map((att: any) => (
                <li key={att.id}>
                  <a
                    href={att.blossom_url}
                    className="text-blue-600 hover:underline"
                  >
                    {att.filename}
                  </a>
                  <span className="text-sm text-gray-600 ml-2">
                    ({(att.size_bytes / 1024).toFixed(2)} KB)
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )}

        <div className="mt-6 flex space-x-3">
          <button className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700">
            Reply
          </button>
          <button className="px-4 py-2 text-gray-700 border border-gray-300 rounded hover:bg-gray-50">
            Delete
          </button>
        </div>
      </div>
    </div>
  )
}
