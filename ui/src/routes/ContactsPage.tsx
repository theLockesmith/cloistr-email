import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { contactAPI, type Contact } from '../lib/api'

export default function ContactsPage() {
  const queryClient = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')

  const { data: response, isLoading } = useQuery({
    queryKey: ['contacts'],
    queryFn: () => contactAPI.list(),
  })

  const addMutation = useMutation({
    mutationFn: (data: Partial<Contact>) => contactAPI.add(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['contacts'] })
    },
  })

  const contacts = response?.data?.contacts || []

  const handleAddContact = async () => {
    if (!email || !name) {
      alert('Please fill in all fields')
      return
    }

    await addMutation.mutateAsync({ email, name })
    setEmail('')
    setName('')
    setShowForm(false)
  }

  return (
    <div className="p-6">
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-3xl font-bold">Contacts</h1>
        <button
          onClick={() => setShowForm(!showForm)}
          className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
        >
          Add Contact
        </button>
      </div>

      {showForm && (
        <div className="bg-white rounded shadow p-6 mb-6 max-w-md">
          <div className="mb-4">
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Email
            </label>
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded"
            />
          </div>

          <div className="mb-4">
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Name
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded"
            />
          </div>

          <div className="flex space-x-3">
            <button
              onClick={handleAddContact}
              disabled={addMutation.isPending}
              className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:opacity-50"
            >
              Add
            </button>
            <button
              onClick={() => setShowForm(false)}
              className="px-4 py-2 text-gray-700 border border-gray-300 rounded hover:bg-gray-50"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {isLoading && <div className="text-center py-8">Loading contacts...</div>}

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {contacts.map((contact: any) => (
          <div
            key={contact.id}
            className="bg-white rounded shadow p-4"
          >
            <h3 className="font-semibold text-gray-900">{contact.name}</h3>
            <p className="text-sm text-gray-600">{contact.email}</p>
            {contact.npub && (
              <p className="text-xs text-gray-500 mt-2 truncate">
                {contact.npub}
              </p>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
