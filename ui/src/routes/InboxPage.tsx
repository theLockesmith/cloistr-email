import { useState, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { emailAPI, type Email } from '../lib/api'
import { parseSearchQuery, hasOperators } from '../lib/search-operators'
import { loadFilterRules, applyFilterRules } from '../lib/filters'

type Folder = 'inbox' | 'sent' | 'drafts' | 'trash' | 'archive' | 'starred'

const STAR_LABEL = '\\Starred'

export default function InboxPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const queryClient = useQueryClient()

  const folder = (searchParams.get('folder') as Folder) || 'inbox'
  const page = parseInt(searchParams.get('page') || '1', 10)
  const search = searchParams.get('search') || ''
  const labelFilter = searchParams.get('label') || ''

  const [searchInput, setSearchInput] = useState(search)
  // Multi-select state
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [showBulkBar, setShowBulkBar] = useState(false)

  // Build API params from folder + search operators
  const buildParams = useCallback(() => {
    const base: Record<string, unknown> = { page, limit: 25 }

    if (folder === 'inbox') {
      base.folder = 'INBOX'
    } else if (folder === 'sent') {
      base.direction = 'sent'
    } else if (folder === 'drafts') {
      base.direction = 'draft'
    } else if (folder === 'trash') {
      base.status = 'deleted'
    } else if (folder === 'archive') {
      base.folder = 'archive'
    } else if (folder === 'starred') {
      base.starred = true
    }

    if (labelFilter) {
      // label filter via the search text — backend supports label param
      base.label = labelFilter
    }

    if (search) {
      if (hasOperators(search)) {
        const parsed = parseSearchQuery(search)
        Object.assign(base, parsed.params)
      } else {
        base.search = search
      }
    }

    return base as Parameters<typeof emailAPI.list>[0]
  }, [folder, page, search, labelFilter])

  const params = buildParams()

  const { data: response, isLoading, error } = useQuery({
    queryKey: ['emails', folder, page, search, labelFilter],
    queryFn: () => emailAPI.list(params),
  })

  const rawEmails = response?.data?.emails || []
  const emails = applyFilterRules(rawEmails, loadFilterRules())
  const total = response?.data?.total || 0
  const totalPages = Math.ceil(total / 25)

  // Mutations
  const bulkMutation = useMutation({
    mutationFn: ({ action, folderArg }: { action: string; folderArg?: string }) =>
      emailAPI.bulk(Array.from(selected), action, folderArg),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['emails'] })
      setSelected(new Set())
      setShowBulkBar(false)
    },
  })

  const starMutation = useMutation({
    mutationFn: ({ id, starred }: { id: string; starred: boolean }) =>
      emailAPI.star(id, starred),
    onMutate: ({ id, starred }) => {
      // Optimistic update
      queryClient.setQueryData(
        ['emails', folder, page, search, labelFilter],
        (old: typeof response) => {
          if (!old) return old
          return {
            ...old,
            data: {
              ...old.data,
              emails: old.data.emails.map((e: Email) =>
                e.id === id ? { ...e, is_starred: starred } : e
              ),
            },
          }
        }
      )
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['emails'] })
    },
  })

  const markReadMutation = useMutation({
    mutationFn: ({ id, read }: { id: string; read: boolean }) =>
      read ? emailAPI.markRead(id) : emailAPI.markUnread(id),
    onMutate: ({ id, read }) => {
      queryClient.setQueryData(
        ['emails', folder, page, search, labelFilter],
        (old: typeof response) => {
          if (!old) return old
          return {
            ...old,
            data: {
              ...old.data,
              emails: old.data.emails.map((e: Email) =>
                e.id === id
                  ? { ...e, read_at: read ? new Date().toISOString() : undefined }
                  : e
              ),
            },
          }
        }
      )
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['emails'] })
    },
  })

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault()
    setSearchParams({ folder, search: searchInput, page: '1' })
  }

  const setFolder = (newFolder: Folder) => {
    setSearchParams({ folder: newFolder, page: '1' })
    setSelected(new Set())
  }

  const setPage = (newPage: number) => {
    setSearchParams({ folder, page: newPage.toString(), search })
  }

  const toggleSelect = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      setShowBulkBar(next.size > 0)
      return next
    })
  }

  const toggleSelectAll = () => {
    if (selected.size === emails.length) {
      setSelected(new Set())
      setShowBulkBar(false)
    } else {
      setSelected(new Set(emails.map((e: Email) => e.id)))
      setShowBulkBar(true)
    }
  }

  const handleBulk = (action: string, folderArg?: string) => {
    bulkMutation.mutate({ action, folderArg })
  }

  return (
    <div className="flex flex-col h-full min-h-0">
      {/* Page header */}
      <div className="flex justify-between items-center px-4 pt-4 pb-2 gap-2 flex-wrap">
        <h1 className="text-xl font-bold capitalize">{folder === 'starred' ? 'Starred' : folder}</h1>
        <Link
          to="/compose"
          className="min-h-[44px] min-w-[44px] flex items-center px-4 py-2 bg-cloistr-primary text-white rounded-lg hover:bg-cloistr-primary-hover transition text-sm font-medium"
        >
          Compose
        </Link>
      </div>

      {/* Folder tabs — horizontally scrollable on mobile */}
      <div className="overflow-x-auto border-b">
        <div className="flex gap-0 min-w-max">
          {(['inbox', 'sent', 'drafts', 'starred', 'archive', 'trash'] as Folder[]).map((f) => (
            <button
              key={f}
              onClick={() => setFolder(f)}
              className={`min-h-[44px] px-4 py-2 capitalize text-sm transition whitespace-nowrap ${
                folder === f
                  ? 'border-b-2 border-cloistr-primary text-cloistr-primary font-medium'
                  : 'text-cloistr-text-muted hover:text-cloistr-text hover:bg-cloistr-bg-hover'
              }`}
            >
              {f}
            </button>
          ))}
        </div>
      </div>

      {/* Search bar */}
      <form onSubmit={handleSearch} className="px-4 py-2">
        <div className="flex gap-2">
          <input
            type="text"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder="Search… or use from:alice subject:hello has:attachment"
            className="flex-1 min-w-0 px-3 py-2 border border-cloistr-border rounded-lg focus:ring-2 focus:ring-cloistr-primary focus:border-cloistr-primary text-sm"
          />
          <button
            type="submit"
            className="min-h-[44px] min-w-[44px] px-3 py-2 bg-cloistr-bg-elevated text-cloistr-text rounded-lg hover:bg-cloistr-bg-hover transition text-sm"
          >
            Search
          </button>
          {search && (
            <button
              type="button"
              className="min-h-[44px] min-w-[44px] px-3 py-2 text-cloistr-text-muted hover:text-cloistr-text text-sm"
              onClick={() => {
                setSearchInput('')
                setSearchParams({ folder, page: '1' })
              }}
            >
              ✕
            </button>
          )}
        </div>
        {/* Operator hint */}
        <p className="mt-1 text-xs text-cloistr-text-muted leading-tight">
          Operators: from: to: subject: has:attachment before:YYYY-MM-DD after:YYYY-MM-DD is:unread is:starred
        </p>
      </form>

      {/* Bulk action bar */}
      {showBulkBar && (
        <div className="px-4 py-2 bg-cloistr-bg-elevated border-b flex items-center gap-2 flex-wrap">
          <span className="text-sm text-cloistr-text-muted mr-1">{selected.size} selected</span>
          <button
            onClick={() => handleBulk('read')}
            disabled={bulkMutation.isPending}
            className="min-h-[44px] px-3 py-1 text-sm border border-cloistr-border rounded hover:bg-cloistr-bg-hover transition"
          >
            Mark read
          </button>
          <button
            onClick={() => handleBulk('unread')}
            disabled={bulkMutation.isPending}
            className="min-h-[44px] px-3 py-1 text-sm border border-cloistr-border rounded hover:bg-cloistr-bg-hover transition"
          >
            Mark unread
          </button>
          <button
            onClick={() => handleBulk('archive')}
            disabled={bulkMutation.isPending}
            className="min-h-[44px] px-3 py-1 text-sm border border-cloistr-border rounded hover:bg-cloistr-bg-hover transition"
          >
            Archive
          </button>
          <button
            onClick={() => handleBulk('star')}
            disabled={bulkMutation.isPending}
            className="min-h-[44px] px-3 py-1 text-sm border border-cloistr-border rounded hover:bg-cloistr-bg-hover transition"
          >
            Star
          </button>
          <button
            onClick={() => handleBulk('delete')}
            disabled={bulkMutation.isPending}
            className="min-h-[44px] px-3 py-1 text-sm text-cloistr-error border border-cloistr-error/30 rounded hover:bg-cloistr-error/5 transition"
          >
            Delete
          </button>
          <button
            onClick={() => { setSelected(new Set()); setShowBulkBar(false) }}
            className="min-h-[44px] ml-auto px-3 py-1 text-sm text-cloistr-text-muted hover:text-cloistr-text"
          >
            Cancel
          </button>
        </div>
      )}

      {/* Loading state */}
      {isLoading && (
        <div className="flex-1 overflow-y-auto">
          {[1, 2, 3, 4, 5].map((i) => (
            <div key={i} className="px-4 py-3 border-b animate-pulse">
              <div className="h-4 bg-cloistr-bg-hover rounded w-1/3 mb-2"></div>
              <div className="h-3 bg-cloistr-bg-hover rounded w-1/4"></div>
            </div>
          ))}
        </div>
      )}

      {/* Error state */}
      {error && (
        <div className="mx-4 my-2 bg-cloistr-error/10 border border-cloistr-error/40 text-cloistr-error rounded-lg p-4 text-sm">
          Error loading emails. Please try again.
        </div>
      )}

      {/* Empty state */}
      {!isLoading && !error && emails.length === 0 && (
        <div className="flex-1 flex flex-col items-center justify-center text-cloistr-text-muted py-12">
          <div className="text-5xl mb-4">📭</div>
          <p className="text-base">
            {search ? 'No emails match your search' : `No emails in ${folder}`}
          </p>
        </div>
      )}

      {/* Email list */}
      {!isLoading && emails.length > 0 && (
        <div className="flex-1 overflow-y-auto">
          {/* Select-all row */}
          <div className="flex items-center gap-2 px-4 py-2 border-b bg-cloistr-bg-elevated text-xs text-cloistr-text-muted">
            <input
              type="checkbox"
              checked={selected.size === emails.length && emails.length > 0}
              onChange={toggleSelectAll}
              aria-label="Select all"
              className="w-4 h-4 accent-cloistr-primary"
            />
            <span>{total} email{total !== 1 ? 's' : ''}</span>
          </div>

          {emails.map((email: Email) => {
            const isUnread = !email.read_at
            const isSelected = selected.has(email.id)
            const isStarred = email.is_starred ||
              (email.labels || []).includes(STAR_LABEL)

            return (
              <div
                key={email.id}
                className={`flex items-center gap-2 px-4 py-3 border-b transition cursor-pointer select-none
                  ${isUnread ? 'bg-cloistr-info/5' : ''}
                  ${isSelected ? 'bg-cloistr-primary/10' : 'hover:bg-cloistr-bg-hover'}`}
              >
                {/* Checkbox — 44px touch target */}
                <button
                  onClick={() => toggleSelect(email.id)}
                  aria-label={isSelected ? 'Deselect' : 'Select'}
                  className="flex-shrink-0 w-11 h-11 flex items-center justify-center -ml-2 -my-1"
                >
                  <input
                    type="checkbox"
                    checked={isSelected}
                    readOnly
                    className="w-4 h-4 accent-cloistr-primary pointer-events-none"
                  />
                </button>

                {/* Star button — 44px touch target */}
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    starMutation.mutate({ id: email.id, starred: !isStarred })
                  }}
                  aria-label={isStarred ? 'Unstar' : 'Star'}
                  className="flex-shrink-0 w-11 h-11 flex items-center justify-center -my-1 text-lg leading-none"
                >
                  <span className={isStarred ? 'text-yellow-400' : 'text-cloistr-text-muted opacity-30'}>
                    ★
                  </span>
                </button>

                {/* Clickable email row */}
                <Link
                  to={`/emails/${email.id}`}
                  className="flex-1 min-w-0 flex flex-col gap-0.5"
                >
                  <div className="flex items-center gap-2 justify-between">
                    <span className={`truncate text-sm ${isUnread ? 'font-semibold' : 'font-medium'} text-cloistr-text`}>
                      {folder === 'sent' ? `To: ${email.to}` : email.from}
                    </span>
                    <span className="flex-shrink-0 text-xs text-cloistr-text-muted ml-2">
                      {formatDate(email.created_at)}
                    </span>
                  </div>
                  <div className="flex items-center gap-1.5 min-w-0">
                    <span className={`truncate text-sm ${isUnread ? 'font-medium' : ''} text-cloistr-text`}>
                      {email.subject || '(No subject)'}
                    </span>
                    {email.is_encrypted && (
                      <span className="flex-shrink-0 text-xs text-cloistr-success" title="Encrypted">🔒</span>
                    )}
                    {email.nostr_verified && (
                      <span className="flex-shrink-0 text-xs text-cloistr-info" title="Verified sender">✓</span>
                    )}
                    {email.has_attachments && (
                      <span className="flex-shrink-0 text-xs text-cloistr-text-muted" title="Has attachments">📎</span>
                    )}
                  </div>
                  {/* Labels */}
                  {email.labels && email.labels.filter(l => l !== STAR_LABEL).length > 0 && (
                    <div className="flex gap-1 flex-wrap mt-0.5">
                      {email.labels.filter(l => l !== STAR_LABEL).map(label => (
                        <span
                          key={label}
                          className="px-1.5 py-0 text-xs bg-cloistr-primary/10 text-cloistr-primary rounded"
                        >
                          {label}
                        </span>
                      ))}
                    </div>
                  )}
                </Link>

                {/* Quick actions: mark read/unread (desktop hover + always visible) */}
                <div className="flex-shrink-0 flex items-center gap-1">
                  <button
                    onClick={(e) => {
                      e.stopPropagation()
                      markReadMutation.mutate({ id: email.id, read: !!email.read_at })
                    }}
                    aria-label={email.read_at ? 'Mark unread' : 'Mark read'}
                    title={email.read_at ? 'Mark unread' : 'Mark read'}
                    className="min-w-[44px] min-h-[44px] flex items-center justify-center text-cloistr-text-muted hover:text-cloistr-text"
                  >
                    {email.read_at ? (
                      <span className="text-xs leading-none">●</span>
                    ) : (
                      <span className="w-2 h-2 rounded-full bg-cloistr-primary inline-block"></span>
                    )}
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="px-4 py-3 border-t flex justify-center items-center gap-2 flex-shrink-0">
          <button
            onClick={() => setPage(page - 1)}
            disabled={page === 1}
            className="min-h-[44px] px-4 py-2 rounded border border-cloistr-border hover:bg-cloistr-bg-hover disabled:opacity-50 disabled:cursor-not-allowed text-sm"
          >
            Previous
          </button>
          <span className="text-sm text-cloistr-text-muted">
            Page {page} of {totalPages}
          </span>
          <button
            onClick={() => setPage(page + 1)}
            disabled={page === totalPages}
            className="min-h-[44px] px-4 py-2 rounded border border-cloistr-border hover:bg-cloistr-bg-hover disabled:opacity-50 disabled:cursor-not-allowed text-sm"
          >
            Next
          </button>
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
