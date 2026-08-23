import { useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { emailAPI, type Email } from '../lib/api'
import {
  hasNostrExtension,
  hasNip44Support,
  decrypt as nip07Decrypt,
  truncatePubkey,
} from '../lib/nostr'

const STAR_LABEL = '\\Starred'

export default function EmailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const [decryptedBody, setDecryptedBody] = useState<string | null>(null)
  const [isDecrypting, setIsDecrypting] = useState(false)
  const [decryptError, setDecryptError] = useState<string | null>(null)
  const [showHtml, setShowHtml] = useState(false)
  const [labelInput, setLabelInput] = useState('')
  const [showLabelInput, setShowLabelInput] = useState(false)

  const hasExtension = hasNostrExtension()
  const hasNip44 = hasNip44Support()

  // Fetch email
  const { data: response, isLoading, error } = useQuery({
    queryKey: ['email', id],
    queryFn: () => (id ? emailAPI.get(id) : Promise.reject('No ID')),
  })

  // Delete mutation
  const deleteMutation = useMutation({
    mutationFn: () => (id ? emailAPI.delete(id) : Promise.reject('No ID')),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['emails'] })
      navigate('/inbox')
    },
  })

  // Archive mutation
  const archiveMutation = useMutation({
    mutationFn: () => (id ? emailAPI.archive(id) : Promise.reject('No ID')),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['emails'] })
      navigate('/inbox')
    },
  })

  // Star mutation
  const starMutation = useMutation({
    mutationFn: ({ starred }: { starred: boolean }) =>
      id ? emailAPI.star(id, starred) : Promise.reject('No ID'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['email', id] })
      queryClient.invalidateQueries({ queryKey: ['emails'] })
    },
  })

  // Mark read/unread mutation
  const readMutation = useMutation({
    mutationFn: ({ read }: { read: boolean }) =>
      id
        ? read ? emailAPI.markRead(id) : emailAPI.markUnread(id)
        : Promise.reject('No ID'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['email', id] })
      queryClient.invalidateQueries({ queryKey: ['emails'] })
    },
  })

  // Add label mutation
  const addLabelMutation = useMutation({
    mutationFn: (label: string) =>
      id ? emailAPI.addLabel(id, label) : Promise.reject('No ID'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['email', id] })
      setLabelInput('')
      setShowLabelInput(false)
    },
  })

  // Remove label mutation
  const removeLabelMutation = useMutation({
    mutationFn: (label: string) =>
      id ? emailAPI.removeLabel(id, label) : Promise.reject('No ID'),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['email', id] })
    },
  })

  const email = response?.data as Email | undefined

  const isStarred = email?.is_starred ||
    (email?.labels || []).includes(STAR_LABEL)
  const userLabels = (email?.labels || []).filter(l => l !== STAR_LABEL)

  // Handle client-side decryption
  const handleDecrypt = async () => {
    if (!email?.encrypted_body || !email?.sender_pubkey) {
      setDecryptError('Missing encrypted body or sender pubkey')
      return
    }
    if (!hasNip44) {
      setDecryptError('Browser extension does not support NIP-44 decryption')
      return
    }
    setIsDecrypting(true)
    setDecryptError(null)
    try {
      const plaintext = await nip07Decrypt(email.sender_pubkey, email.encrypted_body)
      setDecryptedBody(plaintext)
    } catch (err) {
      console.error('Decryption error:', err)
      setDecryptError(err instanceof Error ? err.message : 'Decryption failed')
    } finally {
      setIsDecrypting(false)
    }
  }

  // Download an attachment
  const handleDownloadAttachment = async (attachmentId: string, filename: string) => {
    if (!id) return
    try {
      const res = await emailAPI.getAttachment(id, attachmentId)
      const att = res.data
      if (!att.data_base64) {
        alert('Attachment content not available')
        return
      }
      // Decode base64 and create a blob URL for download
      const binary = atob(att.data_base64)
      const bytes = new Uint8Array(binary.length)
      for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
      const blob = new Blob([bytes], { type: att.content_type || 'application/octet-stream' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = filename
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
    } catch {
      alert('Failed to download attachment')
    }
  }

  const handleDelete = () => {
    if (confirm('Are you sure you want to delete this email?')) {
      deleteMutation.mutate()
    }
  }

  const handleAddLabel = (e: React.FormEvent) => {
    e.preventDefault()
    const label = labelInput.trim()
    if (label) addLabelMutation.mutate(label)
  }

  if (isLoading) {
    return (
      <div className="p-4">
        <div className="animate-pulse">
          <div className="h-7 bg-cloistr-bg-hover rounded w-1/3 mb-4"></div>
          <div className="h-4 bg-cloistr-bg-hover rounded w-1/4 mb-2"></div>
          <div className="h-4 bg-cloistr-bg-hover rounded w-1/4 mb-4"></div>
          <div className="h-32 bg-cloistr-bg-hover rounded"></div>
        </div>
      </div>
    )
  }

  if (error || !email) {
    return (
      <div className="p-4">
        <div className="bg-cloistr-error/10 border border-cloistr-error/40 text-cloistr-error rounded-lg p-4 text-sm">
          Email not found
        </div>
        <button
          onClick={() => navigate('/inbox')}
          className="mt-4 min-h-[44px] px-4 py-2 text-cloistr-text border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover text-sm"
        >
          Back to Inbox
        </button>
      </div>
    )
  }

  const displayBody = decryptedBody || email.body
  const needsDecryption = email.requires_client_decryption && !decryptedBody
  const canDecrypt = hasExtension && hasNip44 && email.encrypted_body && email.sender_pubkey

  return (
    <div className="flex flex-col min-h-0 overflow-y-auto">
      {/* Back + actions bar */}
      <div className="flex items-center gap-2 px-4 py-2 border-b flex-wrap">
        <button
          onClick={() => navigate('/inbox')}
          className="min-h-[44px] min-w-[44px] flex items-center gap-1 px-3 py-2 text-cloistr-text border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover transition text-sm"
        >
          ← Back
        </button>

        {/* Star toggle */}
        <button
          onClick={() => starMutation.mutate({ starred: !isStarred })}
          disabled={starMutation.isPending}
          aria-label={isStarred ? 'Unstar' : 'Star'}
          className="min-h-[44px] min-w-[44px] flex items-center justify-center rounded-lg text-xl leading-none hover:bg-cloistr-bg-hover transition"
        >
          <span className={isStarred ? 'text-yellow-400' : 'text-cloistr-text-muted opacity-50'}>★</span>
        </button>

        {/* Mark read/unread */}
        <button
          onClick={() => readMutation.mutate({ read: !email.read_at })}
          disabled={readMutation.isPending}
          className="min-h-[44px] px-3 py-2 text-sm text-cloistr-text border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover transition"
        >
          {email.read_at ? 'Mark unread' : 'Mark read'}
        </button>

        <div className="flex-1" />

        {/* Thread link — shown when email is a reply */}
        {email.in_reply_to && (
          <span className="text-xs text-cloistr-text-muted truncate max-w-xs">
            Thread
          </span>
        )}
      </div>

      <div className="p-4">
        <div className="bg-cloistr-bg rounded-lg shadow p-4 md:p-6">
          {/* Subject */}
          <h1 className="text-xl md:text-2xl font-bold mb-4 break-words">{email.subject}</h1>

          {/* Email metadata */}
          <div className="border-b pb-4 mb-4">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-1.5 text-sm">
              <div>
                <span className="text-cloistr-text-muted">From:</span>{' '}
                <span className="font-medium break-all">{email.from}</span>
              </div>
              <div>
                <span className="text-cloistr-text-muted">Date:</span>{' '}
                <span className="font-medium">
                  {new Date(email.created_at).toLocaleString()}
                </span>
              </div>
              <div>
                <span className="text-cloistr-text-muted">To:</span>{' '}
                <span className="font-medium break-all">
                  {Array.isArray(email.to) ? email.to.join(', ') : email.to}
                </span>
              </div>
              {email.read_at && (
                <div>
                  <span className="text-cloistr-text-muted">Read:</span>{' '}
                  <span className="font-medium">
                    {new Date(email.read_at).toLocaleString()}
                  </span>
                </div>
              )}
            </div>

            {/* Badges */}
            <div className="mt-3 flex items-center gap-2 flex-wrap">
              {email.is_encrypted && (
                <span
                  className={`inline-flex items-center px-2.5 py-1 rounded-full text-xs ${
                    decryptedBody
                      ? 'bg-cloistr-success/10 text-cloistr-success'
                      : needsDecryption
                      ? 'bg-cloistr-warning/10 text-cloistr-warning'
                      : 'bg-cloistr-success/10 text-cloistr-success'
                  }`}
                >
                  {decryptedBody ? 'Decrypted' : needsDecryption ? 'Encrypted (tap to decrypt)' : 'Encrypted'}
                </span>
              )}
              {email.nostr_verified && (
                <span className="inline-flex items-center px-2.5 py-1 rounded-full text-xs bg-cloistr-info/10 text-cloistr-info">
                  Verified sender
                </span>
              )}
            </div>

            {/* Sender pubkey */}
            {email.sender_pubkey && (
              <div className="mt-2 text-xs text-cloistr-text-muted">
                Sender pubkey: {truncatePubkey(email.sender_pubkey)}
              </div>
            )}

            {/* Labels */}
            <div className="mt-3 flex items-center gap-2 flex-wrap">
              {userLabels.map(label => (
                <span key={label} className="inline-flex items-center gap-1 px-2 py-0.5 text-xs bg-cloistr-primary/10 text-cloistr-primary rounded">
                  {label}
                  <button
                    onClick={() => removeLabelMutation.mutate(label)}
                    aria-label={`Remove label ${label}`}
                    className="ml-0.5 hover:text-cloistr-error leading-none"
                  >
                    ✕
                  </button>
                </span>
              ))}
              <button
                onClick={() => setShowLabelInput(true)}
                className="text-xs text-cloistr-primary hover:text-cloistr-primary-hover"
              >
                + Add label
              </button>
            </div>

            {/* Label input */}
            {showLabelInput && (
              <form onSubmit={handleAddLabel} className="mt-2 flex gap-2">
                <input
                  type="text"
                  value={labelInput}
                  onChange={e => setLabelInput(e.target.value)}
                  placeholder="Label name"
                  className="flex-1 px-2 py-1 text-sm border border-cloistr-border rounded focus:ring-2 focus:ring-cloistr-primary focus:border-cloistr-primary"
                  autoFocus
                />
                <button
                  type="submit"
                  disabled={addLabelMutation.isPending}
                  className="min-h-[44px] px-3 py-1 text-sm bg-cloistr-primary text-white rounded hover:bg-cloistr-primary-hover transition"
                >
                  Add
                </button>
                <button
                  type="button"
                  onClick={() => setShowLabelInput(false)}
                  className="min-h-[44px] px-3 py-1 text-sm text-cloistr-text-muted hover:text-cloistr-text"
                >
                  Cancel
                </button>
              </form>
            )}
          </div>

          {/* Decryption prompt */}
          {needsDecryption && (
            <div className="mb-4 p-4 bg-cloistr-warning/5 border border-cloistr-warning/20 rounded-lg">
              <h3 className="font-medium text-cloistr-warning mb-2">This message is encrypted</h3>
              <p className="text-sm text-cloistr-warning mb-3">
                This email uses client-side encryption. Use your browser extension to decrypt it.
              </p>
              {decryptError && (
                <div className="mb-3 p-3 bg-cloistr-error/10 border border-cloistr-error/30 text-cloistr-error rounded text-sm">
                  {decryptError}
                </div>
              )}
              {hasExtension ? (
                <button
                  onClick={handleDecrypt}
                  disabled={!canDecrypt || isDecrypting}
                  className="min-h-[44px] px-4 py-2 bg-cloistr-warning text-white rounded-lg hover:bg-cloistr-warning-hover disabled:opacity-50 disabled:cursor-not-allowed transition text-sm"
                >
                  {isDecrypting ? 'Decrypting…' : !hasNip44 ? 'NIP-44 not supported' : 'Decrypt Message'}
                </button>
              ) : (
                <p className="text-sm text-cloistr-text-muted italic">
                  Connecting your signer to decrypt…
                </p>
              )}
            </div>
          )}

          {/* Email body */}
          <div className="mb-6">
            {displayBody ? (
              <div className="prose max-w-none whitespace-pre-wrap font-mono text-sm bg-cloistr-bg-elevated p-4 rounded-lg overflow-x-auto">
                {displayBody}
              </div>
            ) : needsDecryption ? (
              <div className="text-cloistr-text-muted italic p-4 bg-cloistr-bg-elevated rounded-lg text-sm">
                [Encrypted content — tap decrypt to view]
              </div>
            ) : (
              <div className="text-cloistr-text-muted italic p-4 bg-cloistr-bg-elevated rounded-lg text-sm">
                [No message body]
              </div>
            )}
          </div>

          {/* HTML body */}
          {email.html_body && !email.is_encrypted && (
            <details
              className="mb-6"
              open={showHtml}
              onToggle={(e) => setShowHtml((e.currentTarget as HTMLDetailsElement).open)}
            >
              <summary className="cursor-pointer text-sm text-cloistr-primary hover:text-cloistr-primary-hover min-h-[44px] flex items-center">
                {showHtml ? 'Hide HTML version' : 'View HTML version'}
              </summary>
              {showHtml && (
                <iframe
                  className="mt-2 w-full border rounded-lg"
                  style={{ minHeight: '300px', height: 'auto' }}
                  srcDoc={email.html_body}
                  sandbox=""
                  title="HTML email body"
                  onLoad={(e) => {
                    const frame = e.currentTarget
                    try {
                      const h = frame.contentDocument?.documentElement.scrollHeight
                      if (h) frame.style.height = `${h + 16}px`
                    } catch {
                      // sandbox restriction — leave default height
                    }
                  }}
                />
              )}
            </details>
          )}

          {/* Attachments */}
          {email.attachments && email.attachments.length > 0 && (
            <div className="mb-6">
              <h3 className="text-sm font-semibold text-cloistr-text mb-2">
                Attachments ({email.attachments.length})
              </h3>
              <div className="flex flex-wrap gap-2">
                {email.attachments.map((att) => (
                  <div
                    key={att.attachment_id}
                    className="flex items-center gap-2 px-3 py-2 bg-cloistr-bg-elevated border border-cloistr-border rounded-lg text-sm"
                  >
                    <span className="text-lg leading-none">📎</span>
                    <div className="min-w-0">
                      <div className="font-medium truncate max-w-[160px]">{att.filename}</div>
                      {att.content_type && (
                        <div className="text-xs text-cloistr-text-muted">{att.content_type}</div>
                      )}
                    </div>
                    <button
                      onClick={() => handleDownloadAttachment(att.attachment_id, att.filename)}
                      className="min-h-[44px] min-w-[44px] flex items-center justify-center text-cloistr-primary hover:text-cloistr-primary-hover transition"
                      aria-label={`Download ${att.filename}`}
                      title="Download"
                    >
                      ⬇
                    </button>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Action buttons */}
          <div className="flex items-center gap-2 pt-4 border-t flex-wrap">
            <button
              onClick={() => navigate(`/compose?reply=${id}`)}
              className="min-h-[44px] px-4 py-2 bg-cloistr-primary text-white rounded-lg hover:bg-cloistr-primary-hover transition text-sm"
            >
              Reply
            </button>
            <button
              onClick={() => navigate(`/compose?forward=${id}`)}
              className="min-h-[44px] px-4 py-2 text-cloistr-text border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover transition text-sm"
            >
              Forward
            </button>
            <button
              onClick={() => archiveMutation.mutate()}
              disabled={archiveMutation.isPending}
              className="min-h-[44px] px-4 py-2 text-cloistr-text border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover disabled:opacity-50 transition text-sm"
            >
              {archiveMutation.isPending ? 'Archiving…' : 'Archive'}
            </button>
            <button
              onClick={handleDelete}
              disabled={deleteMutation.isPending}
              className="min-h-[44px] px-4 py-2 text-cloistr-error border border-cloistr-error/30 rounded-lg hover:bg-cloistr-error/5 disabled:opacity-50 transition text-sm"
            >
              {deleteMutation.isPending ? 'Deleting…' : 'Delete'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
