import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { emailAPI } from '../lib/api'

export default function ComposePage() {
  const [to, setTo] = useState('')
  const [subject, setSubject] = useState('')
  const [body, setBody] = useState('')
  const [encrypt, setEncrypt] = useState(false)
  const navigate = useNavigate()

  const sendMutation = useMutation({
    mutationFn: () =>
      emailAPI.send({
        to,
        subject,
        body,
        encrypt,
      }),
    onSuccess: () => {
      navigate('/inbox')
    },
  })

  const handleSend = async () => {
    if (!to || !subject || !body) {
      alert('Please fill in all fields')
      return
    }

    await sendMutation.mutateAsync()
  }

  return (
    <div className="p-6">
      <h1 className="text-3xl font-bold mb-6">Compose Email</h1>

      <div className="bg-white rounded shadow p-6 max-w-2xl">
        <div className="mb-4">
          <label className="block text-sm font-medium text-gray-700 mb-1">
            To
          </label>
          <input
            type="email"
            value={to}
            onChange={(e) => setTo(e.target.value)}
            placeholder="recipient@example.com"
            className="w-full px-3 py-2 border border-gray-300 rounded focus:ring-blue-500 focus:border-blue-500"
          />
        </div>

        <div className="mb-4">
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Subject
          </label>
          <input
            type="text"
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            placeholder="Email subject"
            className="w-full px-3 py-2 border border-gray-300 rounded focus:ring-blue-500 focus:border-blue-500"
          />
        </div>

        <div className="mb-4">
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Message
          </label>
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Email body"
            rows={10}
            className="w-full px-3 py-2 border border-gray-300 rounded focus:ring-blue-500 focus:border-blue-500"
          />
        </div>

        <div className="mb-6">
          <label className="flex items-center">
            <input
              type="checkbox"
              checked={encrypt}
              onChange={(e) => setEncrypt(e.target.checked)}
              className="w-4 h-4 text-blue-600"
            />
            <span className="ml-2 text-sm text-gray-700">
              Encrypt with Nostr (NIP-44)
            </span>
          </label>
        </div>

        {sendMutation.error && (
          <div className="mb-4 p-4 bg-red-100 border border-red-400 text-red-700 rounded">
            {sendMutation.error instanceof Error
              ? sendMutation.error.message
              : 'Error sending email'}
          </div>
        )}

        <div className="flex space-x-3">
          <button
            onClick={handleSend}
            disabled={sendMutation.isPending}
            className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
          >
            {sendMutation.isPending ? 'Sending...' : 'Send'}
          </button>
          <button
            onClick={() => navigate('/inbox')}
            className="px-4 py-2 text-gray-700 border border-gray-300 rounded hover:bg-gray-50"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  )
}
