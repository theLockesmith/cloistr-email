import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { emailAPI, type Email } from '../lib/api'

type Folder = 'inbox' | 'sent' | 'drafts' | 'trash'

export default function InboxPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const folder = (searchParams.get('folder') as Folder) || 'inbox'
  const page = parseInt(searchParams.get('page') || '1', 10)
  const search = searchParams.get('search') || ''

  const [searchInput, setSearchInput] = useState(search)

  const { data: response, isLoading, error } = useQuery({
    queryKey: ['emails', folder, page, search],
    queryFn: () =>
      emailAPI.list({
        folder: folder === 'inbox' ? undefined : folder,
        direction: folder === 'sent' ? 'sent' : folder === 'inbox' ? 'received' : undefined,
        page,
        limit: 25,
        search: search || undefined,
      }),
  })

  const emails = response?.data?.emails || []
  const total = response?.data?.total || 0
  const totalPages = Math.ceil(total / 25)

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault()
    setSearchParams({ folder, search: searchInput, page: '1' })
  }

  const setFolder = (newFolder: Folder) => {
    setSearchParams({ folder: newFolder, page: '1' })
  }

  const setPage = (newPage: number) => {
    setSearchParams({ folder, page: newPage.toString(), search })
  }

  return (
    <div className="p-6">
      {/* Header */}
      <div className="flex justify-between items-center mb-6">
        <h1 className="text-3xl font-bold capitalize">{folder}</h1>
        <Link
          to="/compose"
          className="px-4 py-2 bg-cloistr-primary text-white rounded-lg hover:bg-cloistr-primary-hover transition"
        >
          Compose
        </Link>
      </div>

      {/* Folder tabs */}
      <div className="flex gap-1 mb-4 border-b">
        {(['inbox', 'sent', 'drafts', 'trash'] as Folder[]).map((f) => (
          <button
            key={f}
            onClick={() => setFolder(f)}
            className={`px-4 py-2 capitalize rounded-t-lg transition ${
              folder === f
                ? 'bg-cloistr-bg border-t border-l border-r -mb-px font-medium'
                : 'text-cloistr-text-muted hover:text-cloistr-text hover:bg-cloistr-bg-hover'
            }`}
          >
            {f}
          </button>
        ))}
      </div>

      {/* Search bar */}
      <form onSubmit={handleSearch} className="mb-4">
        <div className="flex gap-2">
          <input
            type="text"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder="Search emails..."
            className="flex-1 px-3 py-2 border border-cloistr-border rounded-lg focus:ring-2 focus:ring-cloistr-primary focus:border-cloistr-primary"
          />
          <button
            type="submit"
            className="px-4 py-2 bg-cloistr-bg-elevated text-cloistr-text rounded-lg hover:bg-cloistr-bg-hover transition"
          >
            Search
          </button>
          {search && (
            <button
              type="button"
              onClick={() => {
                setSearchInput('')
                setSearchParams({ folder, page: '1' })
              }}
              className="px-4 py-2 text-cloistr-text-muted hover:text-cloistr-text"
            >
              Clear
            </button>
          )}
        </div>
      </form>

      {/* Loading state */}
      {isLoading && (
        <div className="bg-cloistr-bg rounded-lg shadow">
          {[1, 2, 3, 4, 5].map((i) => (
            <div key={i} className="px-6 py-4 border-b animate-pulse">
              <div className="h-5 bg-cloistr-bg-hover rounded w-1/3 mb-2"></div>
              <div className="h-4 bg-cloistr-bg-hover rounded w-1/4"></div>
            </div>
          ))}
        </div>
      )}

      {/* Error state */}
      {error && (
        <div className="bg-cloistr-error/10 border border-cloistr-error/40 text-cloistr-error rounded-lg p-4">
          Error loading emails. Please try again.
        </div>
      )}

      {/* Empty state */}
      {!isLoading && !error && emails.length === 0 && (
        <div className="text-center py-12 text-cloistr-text-muted">
          <div className="text-5xl mb-4">📭</div>
          <p className="text-lg">
            {search ? 'No emails match your search' : `No emails in ${folder}`}
          </p>
        </div>
      )}

      {/* Email list */}
      {!isLoading && emails.length > 0 && (
        <div className="bg-cloistr-bg rounded-lg shadow">
          {emails.map((email: Email) => (
            <Link
              key={email.id}
              to={`/emails/${email.id}`}
              className={`block px-6 py-4 border-b hover:bg-cloistr-bg-hover transition ${
                !email.read_at ? 'bg-cloistr-info/10' : ''
              }`}
            >
              <div className="flex justify-between items-start gap-4">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <h3
                      className={`truncate ${
                        !email.read_at ? 'font-semibold' : 'font-medium'
                      } text-cloistr-text`}
                    >
                      {email.subject || '(No subject)'}
                    </h3>
                    {email.is_encrypted && (
                      <span className="flex-shrink-0 text-cloistr-success" title="Encrypted">
                        🔒
                      </span>
                    )}
                    {email.nostr_verified && (
                      <span className="flex-shrink-0 text-cloistr-info" title="Verified sender (Nostr signature)">
                        ✓
                      </span>
                    )}
                  </div>
                  <p className="text-sm text-cloistr-text-muted truncate">
                    {folder === 'sent' ? `To: ${email.to}` : email.from}
                  </p>
                </div>
                <div className="flex-shrink-0 text-right">
                  <span className="text-sm text-cloistr-text-muted">
                    {formatDate(email.created_at)}
                  </span>
                </div>
              </div>
            </Link>
          ))}
        </div>
      )}

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="mt-4 flex justify-center items-center gap-2">
          <button
            onClick={() => setPage(page - 1)}
            disabled={page === 1}
            className="px-3 py-1 rounded border border-cloistr-border hover:bg-cloistr-bg-hover disabled:opacity-50 disabled:cursor-not-allowed"
          >
            Previous
          </button>
          <span className="text-sm text-cloistr-text-muted">
            Page {page} of {totalPages}
          </span>
          <button
            onClick={() => setPage(page + 1)}
            disabled={page === totalPages}
            className="px-3 py-1 rounded border border-cloistr-border hover:bg-cloistr-bg-hover disabled:opacity-50 disabled:cursor-not-allowed"
          >
            Next
          </button>
        </div>
      )}

      {/* Total count */}
      {total > 0 && (
        <div className="mt-2 text-center text-sm text-cloistr-text-muted">
          {total} email{total !== 1 ? 's' : ''}
        </div>
      )}
    </div>
  )
}

function formatDate(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))

  if (diffDays === 0) {
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  } else if (diffDays === 1) {
    return 'Yesterday'
  } else if (diffDays < 7) {
    return date.toLocaleDateString([], { weekday: 'short' })
  } else {
    return date.toLocaleDateString([], { month: 'short', day: 'numeric' })
  }
}
