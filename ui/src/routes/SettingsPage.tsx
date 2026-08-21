import { useState, useEffect } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { encryptionAPI, keyAPI } from '../lib/api'
import type { EncryptionMode } from '../lib/nostr'

export default function SettingsPage() {
  const [encryptionMethod, setEncryptionMethod] = useState<EncryptionMode>('none')
  const [defaultEncrypt, setDefaultEncrypt] = useState(false)
  const [promptNonNostr, setPromptNonNostr] = useState(true)
  const [signature, setSignature] = useState('')
  const [saveMsg, setSaveMsg] = useState<string | null>(null)
  const [publicKey, setPublicKey] = useState<string | null>(null)

  // Load current encryption capabilities / preferred mode
  const { data: capResponse } = useQuery({
    queryKey: ['encryption-capabilities'],
    queryFn: () => encryptionAPI.getCapabilities(),
  })

  useEffect(() => {
    if (capResponse?.data?.preferred_mode) {
      setEncryptionMethod(capResponse.data.preferred_mode)
    }
    // Load locally-stored preferences (defaultEncrypt, promptNonNostr, signature)
    const stored = {
      defaultEncrypt: localStorage.getItem('email_default_encrypt') === 'true',
      promptNonNostr: localStorage.getItem('email_prompt_non_nostr') !== 'false',
      signature: localStorage.getItem('email_signature') || '',
    }
    setDefaultEncrypt(stored.defaultEncrypt)
    setPromptNonNostr(stored.promptNonNostr)
    setSignature(stored.signature)
  }, [capResponse])

  // Save settings mutation
  const saveMutation = useMutation({
    mutationFn: async () => {
      // Persist preferred encryption mode to the backend
      await encryptionAPI.setPreferredMode(encryptionMethod)
      // Persist UI preferences and signature locally (no backend schema for these yet)
      localStorage.setItem('email_default_encrypt', String(defaultEncrypt))
      localStorage.setItem('email_prompt_non_nostr', String(promptNonNostr))
      localStorage.setItem('email_signature', signature)
    },
    onSuccess: () => {
      setSaveMsg('Settings saved')
      setTimeout(() => setSaveMsg(null), 3000)
    },
    onError: () => {
      setSaveMsg('Failed to save settings')
      setTimeout(() => setSaveMsg(null), 3000)
    },
  })

  const handleViewPublicKey = async () => {
    try {
      const res = await keyAPI.getMyKey()
      setPublicKey(res.data.npub)
    } catch {
      setPublicKey('Could not load public key')
    }
  }

  return (
    <div className="p-6">
      <h1 className="text-3xl font-bold mb-6">Settings</h1>

      <div className="bg-cloistr-bg rounded shadow p-6 max-w-2xl">
        <h2 className="text-xl font-semibold mb-4">Account Settings</h2>

        <div className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-cloistr-text mb-1">
              Encryption Method
            </label>
            <select
              value={encryptionMethod}
              onChange={(e) => setEncryptionMethod(e.target.value as EncryptionMode)}
              className="px-3 py-2 border border-cloistr-border rounded"
            >
              <option value="none">None (plaintext)</option>
              <option value="server">NIP-46 server-side (Recommended)</option>
              <option value="client">NIP-07 client-side (Zero-knowledge)</option>
            </select>
            {capResponse?.data && !capResponse.data.has_nip46 && encryptionMethod === 'server' && (
              <p className="mt-1 text-xs text-cloistr-warning">
                Server-side encryption requires an active NIP-46 bunker connection.
              </p>
            )}
          </div>

          <div>
            <label className="block text-sm font-medium text-cloistr-text mb-1">
              Default Encryption
            </label>
            <label className="flex items-center cursor-pointer">
              <input
                type="checkbox"
                checked={defaultEncrypt}
                onChange={(e) => setDefaultEncrypt(e.target.checked)}
                className="w-4 h-4"
              />
              <span className="ml-2 text-sm text-cloistr-text">
                Always encrypt emails by default
              </span>
            </label>
          </div>

          <div>
            <label className="block text-sm font-medium text-cloistr-text mb-1">
              Prompt for Non-Nostr Recipients
            </label>
            <label className="flex items-center cursor-pointer">
              <input
                type="checkbox"
                checked={promptNonNostr}
                onChange={(e) => setPromptNonNostr(e.target.checked)}
                className="w-4 h-4"
              />
              <span className="ml-2 text-sm text-cloistr-text">
                Ask before sending to recipients without Nostr keys
              </span>
            </label>
          </div>

          <div>
            <label className="block text-sm font-medium text-cloistr-text mb-1">
              Email Signature
            </label>
            <textarea
              value={signature}
              onChange={(e) => setSignature(e.target.value)}
              placeholder="Your signature text (appended after -- on all outgoing emails)"
              rows={4}
              className="w-full px-3 py-2 border border-cloistr-border rounded font-mono text-sm"
            />
            <p className="mt-1 text-xs text-cloistr-text-muted">
              Signature is stored locally in your browser and prepended to compose, reply, and forward.
            </p>
          </div>
        </div>

        <div className="mt-6 pt-6 border-t">
          <h3 className="text-lg font-semibold mb-4">Nostr Identity</h3>
          <p className="text-sm text-cloistr-text-muted mb-4">
            Your email is encrypted and verified using your Nostr keypair.
          </p>
          <button
            onClick={handleViewPublicKey}
            className="px-4 py-2 text-cloistr-text border border-cloistr-border rounded hover:bg-cloistr-bg-hover"
          >
            View My Public Key
          </button>
          {publicKey && (
            <div className="mt-3 p-3 bg-cloistr-bg-elevated rounded font-mono text-xs break-all">
              {publicKey}
            </div>
          )}
          {capResponse?.data && (
            <div className="mt-3 text-sm text-cloistr-text-muted space-y-1">
              <div>
                NIP-46 bunker:{' '}
                <span
                  className={
                    capResponse.data.has_nip46 ? 'text-cloistr-success' : 'text-cloistr-warning'
                  }
                >
                  {capResponse.data.has_nip46 ? 'Connected' : 'Not connected'}
                </span>
              </div>
              <div>
                Can server-encrypt:{' '}
                <span>{capResponse.data.can_server_encrypt ? 'Yes' : 'No'}</span>
              </div>
            </div>
          )}
        </div>

        <div className="mt-6 flex items-center space-x-3">
          <button
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending}
            className="px-4 py-2 bg-cloistr-primary text-white rounded hover:bg-cloistr-primary-hover disabled:opacity-50"
          >
            {saveMutation.isPending ? 'Saving...' : 'Save Settings'}
          </button>
          <button
            onClick={() => {
              // Reset to loaded values
              if (capResponse?.data?.preferred_mode) {
                setEncryptionMethod(capResponse.data.preferred_mode)
              }
              setDefaultEncrypt(localStorage.getItem('email_default_encrypt') === 'true')
              setPromptNonNostr(localStorage.getItem('email_prompt_non_nostr') !== 'false')
              setSignature(localStorage.getItem('email_signature') || '')
              setSaveMsg(null)
            }}
            className="px-4 py-2 text-cloistr-text border border-cloistr-border rounded hover:bg-cloistr-bg-hover"
          >
            Cancel
          </button>
          {saveMsg && (
            <span
              className={`text-sm ${saveMsg.startsWith('Failed') ? 'text-cloistr-error' : 'text-cloistr-success'}`}
            >
              {saveMsg}
            </span>
          )}
        </div>
      </div>
    </div>
  )
}
