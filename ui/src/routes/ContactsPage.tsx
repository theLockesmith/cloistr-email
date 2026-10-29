import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import { contactAPI, type Contact } from '../lib/api'

// The server's reason ("that address is already in your contacts"), not
// "Request failed with status code 409".
function reason(err: unknown): string {
  if (isAxiosError(err)) {
    const msg = (err.response?.data as { message?: string } | undefined)?.message
    if (msg) return msg
  }
  return 'Something went wrong. Please try again.'
}

function ContactCard({ contact }: { contact: Contact }) {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [email, setEmail] = useState(contact.email)
  const [name, setName] = useState(contact.name ?? '')
  const [error, setError] = useState('')

  const saveMutation = useMutation({
    mutationFn: () => contactAPI.update(contact.id, { email: email.trim(), name: name.trim() }),
    onSuccess: () => {
      setEditing(false)
      setError('')
      queryClient.invalidateQueries({ queryKey: ['contacts'] })
    },
    onError: (err) => setError(reason(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: () => contactAPI.delete(contact.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['contacts'] }),
    onError: (err) => setError(reason(err)),
  })

  const label = contact.name || contact.email
  const idBase = `contact-${contact.id}`

  return (
    <div data-testid="contact-card" className="bg-cloistr-bg rounded shadow p-4">
      {editing ? (
        <div className="space-y-3">
          <div>
            <label htmlFor={`${idBase}-email`} className="block text-sm font-medium text-cloistr-text mb-1">
              Email
            </label>
            <input
              id={`${idBase}-email`}
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="w-full px-3 py-2 border border-cloistr-border rounded"
            />
          </div>
          <div>
            <label htmlFor={`${idBase}-name`} className="block text-sm font-medium text-cloistr-text mb-1">
              Name
            </label>
            <input
              id={`${idBase}-name`}
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-full px-3 py-2 border border-cloistr-border rounded"
            />
          </div>
          <div className="flex space-x-3">
            <button
              onClick={() => saveMutation.mutate()}
              disabled={saveMutation.isPending || !email.trim()}
              className="px-3 py-1 bg-cloistr-primary text-white rounded hover:bg-cloistr-primary-hover disabled:opacity-50"
            >
              Save
            </button>
            <button
              onClick={() => {
                setEditing(false)
                setEmail(contact.email)
                setName(contact.name ?? '')
                setError('')
              }}
              className="px-3 py-1 text-cloistr-text border border-cloistr-border rounded hover:bg-cloistr-bg-hover"
            >
              Cancel
            </button>
          </div>
        </div>
      ) : (
        <>
          <h3 className="font-semibold text-cloistr-text break-words">{label}</h3>
          {contact.name && <p className="text-sm text-cloistr-text-muted break-all">{contact.email}</p>}
          {contact.npub && (
            <p className="text-xs text-cloistr-text-muted mt-2 truncate">{contact.npub}</p>
          )}
          <div className="flex space-x-3 mt-3">
            <button
              onClick={() => setEditing(true)}
              aria-label={`Edit ${label}`}
              className="text-sm text-cloistr-primary hover:underline"
            >
              Edit
            </button>
            <button
              onClick={() => {
                if (window.confirm(`Delete ${label} from your contacts?`)) deleteMutation.mutate()
              }}
              disabled={deleteMutation.isPending}
              aria-label={`Delete ${label}`}
              className="text-sm text-cloistr-error hover:underline disabled:opacity-50"
            >
              Delete
            </button>
          </div>
        </>
      )}
      {error && (
        <p role="alert" className="text-sm text-cloistr-error mt-2">
          {error}
        </p>
      )}
    </div>
  )
}

export default function ContactsPage() {
  const queryClient = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [error, setError] = useState('')

  const { data: response, isLoading } = useQuery({
    queryKey: ['contacts'],
    queryFn: () => contactAPI.list(),
  })

  const addMutation = useMutation({
    mutationFn: (data: Partial<Contact>) => contactAPI.add(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['contacts'] })
      setEmail('')
      setName('')
      setError('')
      setShowForm(false)
    },
    onError: (err) => setError(reason(err)),
  })

  const contacts = response?.data?.contacts || []

  const handleAddContact = () => {
    if (!email.trim()) {
      setError('Enter an email address.')
      return
    }
    addMutation.mutate({ email: email.trim(), name: name.trim() })
  }

  return (
    <div className="p-6">
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-3xl font-bold">Contacts</h1>
        <button
          onClick={() => setShowForm(!showForm)}
          className="px-4 py-2 bg-cloistr-primary text-white rounded hover:bg-cloistr-primary-hover"
        >
          Add Contact
        </button>
      </div>

      {showForm && (
        <div className="bg-cloistr-bg rounded shadow p-6 mb-6 max-w-md">
          <div className="mb-4">
            <label htmlFor="new-contact-email" className="block text-sm font-medium text-cloistr-text mb-1">
              Email
            </label>
            <input
              id="new-contact-email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="w-full px-3 py-2 border border-cloistr-border rounded"
            />
          </div>

          <div className="mb-4">
            <label htmlFor="new-contact-name" className="block text-sm font-medium text-cloistr-text mb-1">
              Name (optional)
            </label>
            <input
              id="new-contact-name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-full px-3 py-2 border border-cloistr-border rounded"
            />
          </div>

          {error && (
            <p role="alert" className="text-sm text-cloistr-error mb-4">
              {error}
            </p>
          )}

          <div className="flex space-x-3">
            <button
              onClick={handleAddContact}
              disabled={addMutation.isPending}
              className="px-4 py-2 bg-cloistr-primary text-white rounded hover:bg-cloistr-primary-hover disabled:opacity-50"
            >
              Add
            </button>
            <button
              onClick={() => {
                setShowForm(false)
                setError('')
              }}
              className="px-4 py-2 text-cloistr-text border border-cloistr-border rounded hover:bg-cloistr-bg-hover"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {isLoading && <div className="text-center py-8">Loading contacts...</div>}

      {!isLoading && contacts.length === 0 && (
        <p className="text-cloistr-text-muted">No contacts yet. Add someone you write to often.</p>
      )}

      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {contacts.map((contact) => (
          <ContactCard key={contact.id} contact={contact} />
        ))}
      </div>
    </div>
  )
}
