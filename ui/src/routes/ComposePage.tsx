import { useState, useEffect, useRef } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery } from '@tanstack/react-query'
import { emailAPI, keyAPI, type SendEmailRequest, type Email } from '../lib/api'
import {
  hasNostrExtension,
  hasNip44Support,
  encrypt as nip07Encrypt,
  type EncryptionMode,
} from '../lib/nostr'
import { quoteBody, forwardBlock, replySubject, forwardSubject } from '../lib/email-compose'
import { composeDraftKey, saveDraft, loadDraft, clearDraft, hasDraftContent } from '../lib/draft-autosave'

export default function ComposePage() {
  const [searchParams] = useSearchParams()
  const replyId = searchParams.get('reply')
  const forwardId = searchParams.get('forward')
  const originalId = replyId || forwardId

  const [to, setTo] = useState('')
  const [cc, setCc] = useState('')
  const [subject, setSubject] = useState('')
  const [body, setBody] = useState('')
  const [encryptionMode, setEncryptionMode] = useState<EncryptionMode>('none')
  const [showCc, setShowCc] = useState(false)
  const [recipientPubkey, setRecipientPubkey] = useState<string | null>(null)
  const [isDiscovering, setIsDiscovering] = useState(false)
  const [discoveryError, setDiscoveryError] = useState<string | null>(null)
  // Track whether the form has been pre-populated from the original email.
  // Using a ref-style boolean in state so the useEffect only fires once.
  const [prefilled, setPrefilled] = useState(false)

  // Draft autosave state
  const draftKey = composeDraftKey({ replyId, forwardId })
  // savedAt timestamp of the restored draft; null means no restoration happened.
  const [draftRestoredAt, setDraftRestoredAt] = useState<number | null>(null)
  // True once the initial draft-restore check has completed.
  // Using a ref (not state) so writing it never triggers a re-render.
  const hasInitializedDraft = useRef(false)

  const navigate = useNavigate()

  // Check available encryption modes
  const hasExtension = hasNostrExtension()
  const hasNip44 = hasNip44Support()

  // Load the original email when replying or forwarding
  const { data: originalResponse } = useQuery({
    queryKey: ['email', originalId],
    queryFn: () => (originalId ? emailAPI.get(originalId) : Promise.reject('No ID')),
    enabled: !!originalId,
  })

  const originalEmail = originalResponse?.data as Email | undefined

  // On mount: attempt to restore a previously saved draft.
  // This runs before any other prefill effect so that a restored draft can set
  // `prefilled = true` and suppress the signature / reply-prefill effects.
  useEffect(() => {
    const draft = loadDraft(draftKey)
    if (draft && hasDraftContent(draft)) {
      setTo(draft.to)
      setCc(draft.cc)
      setSubject(draft.subject)
      setBody(draft.body)
      setEncryptionMode(draft.encryptionMode)
      setShowCc(draft.showCc)
      setPrefilled(true)
      setDraftRestoredAt(draft.savedAt)
    }
    hasInitializedDraft.current = true
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
  // Empty deps: run exactly once on mount. draftKey is derived from URL params
  // which cannot change while the component is mounted.

  // Pre-populate the form once the original email arrives
  useEffect(() => {
    if (!originalEmail || prefilled) return
    setPrefilled(true)

    const signature = localStorage.getItem('email_signature') || ''
    const sigBlock = signature ? `\n\n-- \n${signature}` : ''

    if (replyId) {
      setTo(originalEmail.from)
      setSubject(replySubject(originalEmail.subject))
      const date = new Date(originalEmail.created_at).toLocaleString()
      setBody(sigBlock + quoteBody(originalEmail.from, date, originalEmail.body || ''))
    }

    if (forwardId) {
      setSubject(forwardSubject(originalEmail.subject))
      const date = new Date(originalEmail.created_at).toLocaleString()
      setBody(
        sigBlock +
          forwardBlock(originalEmail.from, date, originalEmail.subject, originalEmail.body || ''),
      )
    }
  }, [originalEmail, replyId, forwardId, prefilled])

  // On a fresh compose (no reply/forward), prepend the signature once
  useEffect(() => {
    if (originalId) return // reply/forward path handles this above
    const signature = localStorage.getItem('email_signature') || ''
    if (signature && !prefilled) {
      setPrefilled(true)
      setBody(`\n\n-- \n${signature}`)
    }
  }, [originalId, prefilled])

  // Debounced autosave: write the current compose state to localStorage
  // 1500 ms after the last change. Only fires after the initial draft-restore
  // check and only when the form has meaningful content.
  useEffect(() => {
    if (!hasInitializedDraft.current) return

    const timer = setTimeout(() => {
      const draft = { to, cc, subject, body, encryptionMode, showCc, savedAt: Date.now() }
      if (hasDraftContent(draft)) {
        saveDraft(draftKey, draft)
      }
    }, 1500)

    return () => clearTimeout(timer)
  }, [to, cc, subject, body, encryptionMode, showCc, draftKey])

  // Discover recipient's pubkey when email changes
  useEffect(() => {
    const discoverKey = async () => {
      if (!to || !to.includes('@')) {
        setRecipientPubkey(null)
        setDiscoveryError(null)
        return
      }

      setIsDiscovering(true)
      setDiscoveryError(null)

      try {
        const response = await keyAPI.discover(to)
        if (response.data.found && response.data.pubkey) {
          setRecipientPubkey(response.data.pubkey)
        } else {
          setRecipientPubkey(null)
          if (encryptionMode !== 'none') {
            setDiscoveryError('Recipient has no known Nostr identity for encryption')
          }
        }
      } catch {
        setRecipientPubkey(null)
        if (encryptionMode !== 'none') {
          setDiscoveryError('Could not discover recipient key')
        }
      } finally {
        setIsDiscovering(false)
      }
    }

    const debounce = setTimeout(discoverKey, 500)
    return () => clearTimeout(debounce)
  }, [to, encryptionMode])

  // Send email mutation
  const sendMutation = useMutation({
    mutationFn: async () => {
      let sendRequest: SendEmailRequest = {
        to: [to],
        subject,
        body,
        encryption_mode: encryptionMode,
        // Include threading headers when replying
        ...(replyId && originalEmail?.message_id
          ? {
              in_reply_to: originalEmail.message_id,
              references: originalEmail.message_id ? [originalEmail.message_id] : [],
            }
          : {}),
      }

      // Add CC if provided
      if (cc) {
        sendRequest.cc = cc
          .split(',')
          .map((e) => e.trim())
          .filter(Boolean)
      }

      // Handle client-side encryption
      if (encryptionMode === 'client') {
        if (!recipientPubkey) {
          throw new Error('Cannot encrypt: recipient public key not found')
        }
        if (!hasNip44) {
          throw new Error('Cannot encrypt: browser extension does not support NIP-44')
        }

        // Encrypt the body using NIP-07 extension
        const encryptedBody = await nip07Encrypt(recipientPubkey, body)

        sendRequest = {
          ...sendRequest,
          body: undefined, // Don't send plaintext
          pre_encrypted_body: encryptedBody,
          recipient_pubkeys: { [to]: recipientPubkey },
        }
      } else if (encryptionMode === 'server' && recipientPubkey) {
        // Server-side encryption - just pass the recipient pubkey
        sendRequest.recipient_pubkeys = { [to]: recipientPubkey }
      }

      return emailAPI.send(sendRequest)
    },
    onSuccess: (response) => {
      if (response.data.status === 'sent') {
        clearDraft(draftKey)
        navigate('/inbox')
      }
    },
  })

  const handleSend = async () => {
    if (!to || !subject || !body) {
      alert('Please fill in all required fields')
      return
    }

    if (encryptionMode !== 'none' && !recipientPubkey) {
      alert('Cannot encrypt: recipient has no known Nostr identity')
      return
    }

    await sendMutation.mutateAsync()
  }

  const canEncrypt = recipientPubkey !== null

  return (
    <div className="p-6">
      <h1 className="text-3xl font-bold mb-6">
        {replyId ? 'Reply' : forwardId ? 'Forward' : 'Compose Email'}
      </h1>

      {/* Draft restored banner */}
      {draftRestoredAt !== null && (
        <div className="mb-4 flex items-center justify-between gap-3 px-4 py-3 bg-amber-50 border border-amber-300 rounded-lg text-amber-800 dark:bg-amber-900/20 dark:border-amber-700 dark:text-amber-300">
          <span className="text-sm font-medium">
            Draft restored from{' '}
            {new Date(draftRestoredAt).toLocaleString(undefined, {
              month: 'short',
              day: 'numeric',
              hour: '2-digit',
              minute: '2-digit',
            })}
          </span>
          <button
            type="button"
            aria-label="Dismiss draft restored notice"
            onClick={() => setDraftRestoredAt(null)}
            className="text-amber-700 hover:text-amber-900 dark:text-amber-400 dark:hover:text-amber-200 text-lg leading-none"
          >
            &times;
          </button>
        </div>
      )}

      <div className="bg-cloistr-bg rounded-lg shadow p-6 max-w-3xl">
        {/* To Field */}
        <div className="mb-4">
          <div className="flex items-center justify-between mb-1">
            <label className="block text-sm font-medium text-cloistr-text">To</label>
            {!showCc && (
              <button
                type="button"
                onClick={() => setShowCc(true)}
                className="text-sm text-cloistr-primary hover:text-cloistr-primary-hover"
              >
                Add CC
              </button>
            )}
          </div>
          <input
            type="email"
            value={to}
            onChange={(e) => setTo(e.target.value)}
            placeholder="recipient@example.com"
            className="w-full px-3 py-2 border border-cloistr-border rounded-lg focus:ring-2 focus:ring-cloistr-primary focus:border-cloistr-primary"
          />
          {/* Recipient key discovery status */}
          {to && (
            <div className="mt-1 text-sm">
              {isDiscovering && (
                <span className="text-cloistr-text-muted">Discovering recipient identity...</span>
              )}
              {!isDiscovering && recipientPubkey && (
                <span className="text-cloistr-success">
                  Nostr identity found - encryption available
                </span>
              )}
              {!isDiscovering && !recipientPubkey && to.includes('@') && (
                <span className="text-cloistr-text-muted">
                  No Nostr identity found - encryption unavailable
                </span>
              )}
              {discoveryError && (
                <span className="text-cloistr-warning">{discoveryError}</span>
              )}
            </div>
          )}
        </div>

        {/* CC Field (optional) */}
        {showCc && (
          <div className="mb-4">
            <label className="block text-sm font-medium text-cloistr-text mb-1">CC</label>
            <input
              type="text"
              value={cc}
              onChange={(e) => setCc(e.target.value)}
              placeholder="email1@example.com, email2@example.com"
              className="w-full px-3 py-2 border border-cloistr-border rounded-lg focus:ring-2 focus:ring-cloistr-primary focus:border-cloistr-primary"
            />
          </div>
        )}

        {/* Subject Field */}
        <div className="mb-4">
          <label className="block text-sm font-medium text-cloistr-text mb-1">
            Subject
          </label>
          <input
            type="text"
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            placeholder="Email subject"
            className="w-full px-3 py-2 border border-cloistr-border rounded-lg focus:ring-2 focus:ring-cloistr-primary focus:border-cloistr-primary"
          />
        </div>

        {/* Body Field */}
        <div className="mb-4">
          <label className="block text-sm font-medium text-cloistr-text mb-1">
            Message
          </label>
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Write your message..."
            rows={12}
            className="w-full px-3 py-2 border border-cloistr-border rounded-lg focus:ring-2 focus:ring-cloistr-primary focus:border-cloistr-primary font-mono text-sm"
          />
        </div>

        {/* Encryption Options */}
        <div className="mb-6 p-4 bg-cloistr-bg-elevated rounded-lg">
          <label className="block text-sm font-medium text-cloistr-text mb-3">
            Encryption
          </label>
          <div className="space-y-3">
            {/* No encryption */}
            <label className="flex items-start cursor-pointer">
              <input
                type="radio"
                name="encryption"
                value="none"
                checked={encryptionMode === 'none'}
                onChange={() => setEncryptionMode('none')}
                className="mt-1 w-4 h-4 text-cloistr-primary"
              />
              <div className="ml-3">
                <span className="font-medium">No encryption</span>
                <p className="text-sm text-cloistr-text-muted">
                  Message sent in plaintext (standard email)
                </p>
              </div>
            </label>

            {/* Server-side encryption */}
            <label
              className={`flex items-start ${canEncrypt ? 'cursor-pointer' : 'opacity-50 cursor-not-allowed'}`}
            >
              <input
                type="radio"
                name="encryption"
                value="server"
                checked={encryptionMode === 'server'}
                onChange={() => canEncrypt && setEncryptionMode('server')}
                disabled={!canEncrypt}
                className="mt-1 w-4 h-4 text-cloistr-primary"
              />
              <div className="ml-3">
                <span className="font-medium">Server-side encryption (NIP-46)</span>
                <p className="text-sm text-cloistr-text-muted">
                  Encrypted using your bunker connection
                  {!canEncrypt && ' - requires recipient Nostr identity'}
                </p>
              </div>
            </label>

            {/* Client-side encryption */}
            <label
              className={`flex items-start ${canEncrypt && hasNip44 ? 'cursor-pointer' : 'opacity-50 cursor-not-allowed'}`}
            >
              <input
                type="radio"
                name="encryption"
                value="client"
                checked={encryptionMode === 'client'}
                onChange={() => canEncrypt && hasNip44 && setEncryptionMode('client')}
                disabled={!canEncrypt || !hasNip44}
                className="mt-1 w-4 h-4 text-cloistr-primary"
              />
              <div className="ml-3">
                <div className="flex items-center gap-2">
                  <span className="font-medium">Client-side encryption (NIP-07)</span>
                  {hasNip44 && (
                    <span className="px-2 py-0.5 bg-cloistr-success/10 text-cloistr-success text-xs rounded">
                      Zero-knowledge
                    </span>
                  )}
                </div>
                <p className="text-sm text-cloistr-text-muted">
                  {hasExtension
                    ? hasNip44
                      ? 'Encrypted locally - server never sees plaintext'
                      : 'Your extension does not support NIP-44 encryption'
                    : 'Requires browser extension with NIP-44 support'}
                  {hasNip44 && !canEncrypt && ' - requires recipient Nostr identity'}
                </p>
              </div>
            </label>
          </div>
        </div>

        {/* Error Display */}
        {sendMutation.error && (
          <div className="mb-4 p-4 bg-cloistr-error/10 border border-cloistr-error/40 text-cloistr-error rounded-lg">
            {sendMutation.error instanceof Error
              ? sendMutation.error.message
              : 'Error sending email'}
          </div>
        )}

        {/* Action Buttons */}
        <div className="flex items-center gap-3">
          <button
            onClick={handleSend}
            disabled={sendMutation.isPending || !to || !subject || !body}
            className="px-6 py-2 bg-cloistr-primary text-white font-medium rounded-lg hover:bg-cloistr-primary-hover disabled:opacity-50 disabled:cursor-not-allowed transition"
          >
            {sendMutation.isPending ? 'Sending...' : 'Send'}
          </button>
          <button
            onClick={() => navigate(replyId || forwardId ? `/emails/${originalId}` : '/inbox')}
            className="px-6 py-2 text-cloistr-text border border-cloistr-border rounded-lg hover:bg-cloistr-bg-hover transition"
          >
            Cancel
          </button>

          {/* Encryption indicator */}
          {encryptionMode !== 'none' && (
            <span className="ml-auto text-sm text-cloistr-text-muted">
              {encryptionMode === 'client' ? 'Client-encrypted' : 'Server-encrypted'}
            </span>
          )}
        </div>
      </div>
    </div>
  )
}
