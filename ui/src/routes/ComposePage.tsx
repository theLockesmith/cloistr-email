import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { emailAPI, keyAPI, type SendEmailRequest } from '../lib/api'
import {
  hasNostrExtension,
  hasNip44Support,
  encrypt as nip07Encrypt,
  type EncryptionMode,
} from '../lib/nostr'

export default function ComposePage() {
  const [to, setTo] = useState('')
  const [cc, setCc] = useState('')
  const [subject, setSubject] = useState('')
  const [body, setBody] = useState('')
  const [encryptionMode, setEncryptionMode] = useState<EncryptionMode>('none')
  const [showCc, setShowCc] = useState(false)
  const [recipientPubkey, setRecipientPubkey] = useState<string | null>(null)
  const [isDiscovering, setIsDiscovering] = useState(false)
  const [discoveryError, setDiscoveryError] = useState<string | null>(null)
  const navigate = useNavigate()

  // Check available encryption modes
  const hasExtension = hasNostrExtension()
  const hasNip44 = hasNip44Support()

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
      } catch (err) {
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
      }

      // Add CC if provided
      if (cc) {
        sendRequest.cc = cc.split(',').map((e) => e.trim()).filter(Boolean)
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
      <h1 className="text-3xl font-bold mb-6">Compose Email</h1>

      <div className="bg-white rounded-lg shadow p-6 max-w-3xl">
        {/* To Field */}
        <div className="mb-4">
          <div className="flex items-center justify-between mb-1">
            <label className="block text-sm font-medium text-gray-700">To</label>
            {!showCc && (
              <button
                type="button"
                onClick={() => setShowCc(true)}
                className="text-sm text-blue-600 hover:text-blue-800"
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
            className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
          />
          {/* Recipient key discovery status */}
          {to && (
            <div className="mt-1 text-sm">
              {isDiscovering && (
                <span className="text-gray-500">Discovering recipient identity...</span>
              )}
              {!isDiscovering && recipientPubkey && (
                <span className="text-green-600">
                  ✓ Nostr identity found - encryption available
                </span>
              )}
              {!isDiscovering && !recipientPubkey && to.includes('@') && (
                <span className="text-gray-500">
                  No Nostr identity found - encryption unavailable
                </span>
              )}
              {discoveryError && (
                <span className="text-amber-600">{discoveryError}</span>
              )}
            </div>
          )}
        </div>

        {/* CC Field (optional) */}
        {showCc && (
          <div className="mb-4">
            <label className="block text-sm font-medium text-gray-700 mb-1">CC</label>
            <input
              type="text"
              value={cc}
              onChange={(e) => setCc(e.target.value)}
              placeholder="email1@example.com, email2@example.com"
              className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
            />
          </div>
        )}

        {/* Subject Field */}
        <div className="mb-4">
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Subject
          </label>
          <input
            type="text"
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            placeholder="Email subject"
            className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
          />
        </div>

        {/* Body Field */}
        <div className="mb-4">
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Message
          </label>
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Write your message..."
            rows={12}
            className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 font-mono text-sm"
          />
        </div>

        {/* Encryption Options */}
        <div className="mb-6 p-4 bg-gray-50 rounded-lg">
          <label className="block text-sm font-medium text-gray-700 mb-3">
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
                className="mt-1 w-4 h-4 text-blue-600"
              />
              <div className="ml-3">
                <span className="font-medium">No encryption</span>
                <p className="text-sm text-gray-500">
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
                className="mt-1 w-4 h-4 text-blue-600"
              />
              <div className="ml-3">
                <span className="font-medium">Server-side encryption (NIP-46)</span>
                <p className="text-sm text-gray-500">
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
                className="mt-1 w-4 h-4 text-blue-600"
              />
              <div className="ml-3">
                <div className="flex items-center gap-2">
                  <span className="font-medium">Client-side encryption (NIP-07)</span>
                  {hasNip44 && (
                    <span className="px-2 py-0.5 bg-green-100 text-green-700 text-xs rounded">
                      Zero-knowledge
                    </span>
                  )}
                </div>
                <p className="text-sm text-gray-500">
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
          <div className="mb-4 p-4 bg-red-100 border border-red-400 text-red-700 rounded-lg">
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
            className="px-6 py-2 bg-blue-600 text-white font-medium rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition"
          >
            {sendMutation.isPending ? 'Sending...' : 'Send'}
          </button>
          <button
            onClick={() => navigate('/inbox')}
            className="px-6 py-2 text-gray-700 border border-gray-300 rounded-lg hover:bg-gray-50 transition"
          >
            Cancel
          </button>

          {/* Encryption indicator */}
          {encryptionMode !== 'none' && (
            <span className="ml-auto text-sm text-gray-600">
              {encryptionMode === 'client' ? '🔐 Client-encrypted' : '🔒 Server-encrypted'}
            </span>
          )}
        </div>
      </div>
    </div>
  )
}
