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

export default function EmailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const [decryptedBody, setDecryptedBody] = useState<string | null>(null)
  const [isDecrypting, setIsDecrypting] = useState(false)
  const [decryptError, setDecryptError] = useState<string | null>(null)

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

  const email = response?.data as Email | undefined

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

  const handleDelete = () => {
    if (confirm('Are you sure you want to delete this email?')) {
      deleteMutation.mutate()
    }
  }

  if (isLoading) {
    return (
      <div className="p-6">
        <div className="animate-pulse">
          <div className="h-8 bg-cloistr-bg-hover rounded w-1/3 mb-4"></div>
          <div className="h-4 bg-cloistr-bg-hover rounded w-1/4 mb-2"></div>
          <div className="h-4 bg-cloistr-bg-hover rounded w-1/4 mb-4"></div>
          <div className="h-32 bg-cloistr-bg-hover rounded"></div>
        </div>
      </div>
    )
  }

  if (error || !email) {
    return (
      <div className="p-6">
        <div className="bg-cloistr-error/10 border border-cloistr-error/40 text-cloistr-error rounded-lg p-4">
          Email not found
        </div>
        <button
          onClick={() => navigate('/inbox')}
          className="mt-4 px-4 py-2 text-cloistr-text border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover"
        >
          Back to Inbox
        </button>
      </div>
    )
  }

  // Determine what body to display
  const displayBody = decryptedBody || email.body
  const needsDecryption = email.requires_client_decryption && !decryptedBody
  const canDecrypt = hasExtension && hasNip44 && email.encrypted_body && email.sender_pubkey

  return (
    <div className="p-6">
      <button
        onClick={() => navigate('/inbox')}
        className="mb-6 px-4 py-2 text-cloistr-text border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover transition"
      >
        ← Back to Inbox
      </button>

      <div className="bg-cloistr-bg rounded-lg shadow p-6">
        {/* Subject */}
        <h1 className="text-2xl font-bold mb-4">{email.subject}</h1>

        {/* Email metadata */}
        <div className="border-b pb-4 mb-4">
          <div className="grid grid-cols-2 gap-2 text-sm">
            <div>
              <span className="text-cloistr-text-muted">From:</span>{' '}
              <span className="font-medium">{email.from}</span>
            </div>
            <div>
              <span className="text-cloistr-text-muted">Date:</span>{' '}
              <span className="font-medium">
                {new Date(email.created_at).toLocaleString()}
              </span>
            </div>
            <div>
              <span className="text-cloistr-text-muted">To:</span>{' '}
              <span className="font-medium">
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

          {/* Encryption and verification badges */}
          <div className="mt-3 flex items-center gap-2 flex-wrap">
            {email.is_encrypted && (
              <span
                className={`inline-flex items-center px-3 py-1 rounded-full text-sm ${
                  decryptedBody
                    ? 'bg-cloistr-success/10 text-cloistr-success'
                    : needsDecryption
                    ? 'bg-cloistr-warning/10 text-cloistr-warning'
                    : 'bg-cloistr-success/10 text-cloistr-success'
                }`}
              >
                {decryptedBody
                  ? '🔓 Decrypted'
                  : needsDecryption
                  ? '🔐 Encrypted (requires decryption)'
                  : '🔒 Encrypted'}
              </span>
            )}
            {email.nostr_verified && (
              <span className="inline-flex items-center px-3 py-1 rounded-full text-sm bg-cloistr-info/10 text-cloistr-info">
                ✓ Verified sender
              </span>
            )}
            {email.encryption_mode && (
              <span className="text-xs text-cloistr-text-muted">
                {email.encryption_mode === 'client'
                  ? 'Client-side (NIP-07)'
                  : email.encryption_mode === 'server'
                  ? 'Server-side (NIP-46)'
                  : ''}
              </span>
            )}
          </div>

          {/* Sender pubkey info */}
          {email.sender_pubkey && (
            <div className="mt-2 text-xs text-cloistr-text-muted">
              Sender pubkey: {truncatePubkey(email.sender_pubkey)}
            </div>
          )}
        </div>

        {/* Decryption prompt for client-encrypted emails */}
        {needsDecryption && (
          <div className="mb-4 p-4 bg-cloistr-warning/5 border border-cloistr-warning/20 rounded-lg">
            <h3 className="font-medium text-cloistr-warning mb-2">
              This message is encrypted
            </h3>
            <p className="text-sm text-cloistr-warning mb-3">
              This email was encrypted using client-side encryption. Click the button
              below to decrypt it using your browser extension.
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
                className="px-4 py-2 bg-cloistr-warning text-white rounded-lg hover:bg-cloistr-warning-hover disabled:opacity-50 disabled:cursor-not-allowed transition"
              >
                {isDecrypting
                  ? 'Decrypting...'
                  : !hasNip44
                  ? 'NIP-44 not supported'
                  : 'Decrypt Message'}
              </button>
            ) : (
              <p className="text-sm text-cloistr-text-muted italic">
                Connecting your signer to decrypt&hellip;
              </p>
            )}

            {hasExtension && !hasNip44 && (
              <p className="mt-2 text-xs text-cloistr-text-muted">
                Your extension does not support NIP-44 decryption
              </p>
            )}
          </div>
        )}

        {/* Email body */}
        <div className="mb-6">
          {displayBody ? (
            <div className="prose max-w-none whitespace-pre-wrap font-mono text-sm bg-cloistr-bg-elevated p-4 rounded-lg">
              {displayBody}
            </div>
          ) : needsDecryption ? (
            <div className="text-cloistr-text-dim italic p-4 bg-cloistr-bg-elevated rounded-lg">
              [Encrypted content - click decrypt to view]
            </div>
          ) : (
            <div className="text-cloistr-text-dim italic p-4 bg-cloistr-bg-elevated rounded-lg">
              [No message body]
            </div>
          )}
        </div>

        {/* HTML body toggle (if available) */}
        {email.html_body && !email.is_encrypted && (
          <details className="mb-6">
            <summary className="cursor-pointer text-sm text-cloistr-primary hover:text-cloistr-primary-hover">
              View HTML version
            </summary>
            <div
              className="mt-2 p-4 border rounded-lg prose max-w-none"
              dangerouslySetInnerHTML={{ __html: email.html_body }}
            />
          </details>
        )}

        {/* Action buttons */}
        <div className="flex items-center gap-3 pt-4 border-t">
          <button
            onClick={() => navigate(`/compose?reply=${id}`)}
            className="px-4 py-2 bg-cloistr-primary text-white rounded-lg hover:bg-cloistr-primary-hover transition"
          >
            Reply
          </button>
          <button
            onClick={() => navigate(`/compose?forward=${id}`)}
            className="px-4 py-2 text-cloistr-text border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover transition"
          >
            Forward
          </button>
          <button
            onClick={handleDelete}
            disabled={deleteMutation.isPending}
            className="px-4 py-2 text-cloistr-error border border-cloistr-error/30 rounded-lg hover:bg-cloistr-error/5 disabled:opacity-50 transition"
          >
            {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
          </button>
        </div>
      </div>
    </div>
  )
}
