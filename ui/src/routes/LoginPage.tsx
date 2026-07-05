import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Header, useBackendAuth } from '@cloistr/ui/components'
import { waitForNostrExtension, hasNip44Support } from '../lib/nostr'

type AuthMethod = 'nip07' | 'nip46' | null

export default function LoginPage() {
  const [authMethod, setAuthMethod] = useState<AuthMethod>(null)
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [bunkerUrl, setBunkerUrl] = useState('')
  const [hasExtension, setHasExtension] = useState(false)
  const [extensionHasNip44, setExtensionHasNip44] = useState(false)
  const navigate = useNavigate()
  const { loginWithExtension, loginWithBunker } = useBackendAuth()

  // Check for NIP-07 extension on mount
  useEffect(() => {
    const checkExtension = async () => {
      const available = await waitForNostrExtension(2000)
      setHasExtension(available)
      if (available) {
        setExtensionHasNip44(hasNip44Support())
      }
    }
    checkExtension()
  }, [])

  // NIP-07 login (browser extension) via the shared backend-auth provider.
  const handleNip07Login = async () => {
    setIsLoading(true)
    setError(null)
    try {
      await loginWithExtension()
      navigate('/inbox')
    } catch (err) {
      console.error('NIP-07 login error:', err)
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setIsLoading(false)
    }
  }

  // NIP-46 login (nsecbunker) via the shared backend-auth provider.
  const handleNip46Login = async () => {
    if (!bunkerUrl) {
      setError('Please enter your bunker URL')
      return
    }
    setIsLoading(true)
    setError(null)
    try {
      await loginWithBunker(bunkerUrl)
      navigate('/inbox')
    } catch (err) {
      console.error('NIP-46 login error:', err)
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setIsLoading(false)
    }
  }

  // Method selection screen
  if (!authMethod) {
    return (
      <div className="min-h-screen flex flex-col bg-cloistr-bg-elevated">
        <Header activeServiceId="email" auth={{ authenticated: false }} />
        <div className="flex flex-1 flex-col items-center justify-center p-4">
        <div className="bg-cloistr-bg rounded-lg shadow-lg p-8 w-[420px]">
          <div className="flex justify-center mb-4">
            <img src="/cloistr-logo.svg" alt="Cloistr" className="h-12" />
          </div>
          <h1 className="text-3xl font-bold text-center mb-2">Cloistr Mail</h1>
          <p className="text-cloistr-text-muted text-center mb-8">
            Secure email with Nostr identity
          </p>

          <div className="space-y-4">
            {/* NIP-07 Browser Extension */}
            <button
              onClick={() => hasExtension ? setAuthMethod('nip07') : undefined}
              disabled={!hasExtension}
              className={`w-full p-4 border-2 rounded-lg text-left transition ${
                hasExtension
                  ? 'border-cloistr-primary hover:bg-cloistr-bg-hover cursor-pointer'
                  : 'border-cloistr-border bg-cloistr-bg-elevated cursor-not-allowed opacity-60'
              }`}
            >
              <div className="flex items-center justify-between">
                <div>
                  <h3 className="font-semibold text-lg">Browser Extension</h3>
                  <p className="text-sm text-cloistr-text-muted">
                    {hasExtension
                      ? 'Use nos2x, Alby, or another NIP-07 extension'
                      : 'No extension detected'}
                  </p>
                </div>
                {hasExtension && (
                  <div className="flex flex-col items-end">
                    <span className="text-cloistr-success text-sm font-medium">Available</span>
                    {extensionHasNip44 && (
                      <span className="text-xs text-cloistr-text-muted">NIP-44 supported</span>
                    )}
                  </div>
                )}
              </div>
            </button>

            {/* NIP-46 Bunker */}
            <button
              onClick={() => setAuthMethod('nip46')}
              className="w-full p-4 border-2 border-cloistr-border rounded-lg text-left hover:bg-cloistr-bg-hover transition"
            >
              <div>
                <h3 className="font-semibold text-lg">nsecBunker</h3>
                <p className="text-sm text-cloistr-text-muted">
                  Connect to a remote signer for enhanced security
                </p>
              </div>
            </button>
          </div>

          <p className="text-xs text-cloistr-text-muted text-center mt-6">
            Your private key never leaves your device or bunker
          </p>
        </div>
      </div>
      </div>
    )
  }

  // NIP-07 login screen
  if (authMethod === 'nip07') {
    return (
      <div className="min-h-screen flex flex-col bg-cloistr-bg-elevated">
        <Header activeServiceId="email" auth={{ authenticated: false }} />
        <div className="flex flex-1 flex-col items-center justify-center p-4">
        <div className="bg-cloistr-bg rounded-lg shadow-lg p-8 w-[420px]">
          <button
            onClick={() => setAuthMethod(null)}
            className="text-cloistr-text-muted hover:text-cloistr-text mb-4"
          >
            ← Back
          </button>

          <h1 className="text-2xl font-bold mb-2">Browser Extension Login</h1>
          <p className="text-cloistr-text-muted mb-6">
            Click the button below to sign in. Your extension will prompt you to approve.
          </p>

          {error && (
            <div className="mb-4 p-4 bg-cloistr-error/10 border border-cloistr-error/40 text-cloistr-error rounded">
              {error}
            </div>
          )}

          <button
            onClick={handleNip07Login}
            disabled={isLoading}
            className="w-full px-4 py-3 bg-cloistr-primary text-white font-medium rounded-lg hover:bg-cloistr-primary-hover disabled:opacity-50 transition"
          >
            {isLoading ? 'Connecting...' : 'Sign in with Extension'}
          </button>

          <div className="mt-6 p-4 bg-cloistr-bg-elevated rounded-lg">
            <h4 className="font-medium text-sm mb-2">What happens:</h4>
            <ul className="text-sm text-cloistr-text-muted space-y-1">
              <li>1. Your extension will request access to your public key</li>
              <li>2. You'll be asked to sign a login message</li>
              <li>3. The server verifies your signature</li>
            </ul>
          </div>
        </div>
      </div>
      </div>
    )
  }

  // NIP-46 login screen
  return (
    <div className="min-h-screen flex flex-col bg-cloistr-bg-elevated">
        <Header activeServiceId="email" auth={{ authenticated: false }} />
        <div className="flex flex-1 flex-col items-center justify-center p-4">
      <div className="bg-cloistr-bg rounded-lg shadow-lg p-8 w-[420px]">
        <button
          onClick={() => setAuthMethod(null)}
          className="text-cloistr-text-muted hover:text-cloistr-text mb-4"
        >
          ← Back
        </button>

        <h1 className="text-2xl font-bold mb-2">nsecBunker Login</h1>
        <p className="text-cloistr-text-muted mb-6">
          Enter your bunker connection URL to sign in securely.
        </p>

        {error && (
          <div className="mb-4 p-4 bg-cloistr-error/10 border border-cloistr-error/40 text-cloistr-error rounded">
            {error}
          </div>
        )}

        <div className="mb-4">
          <label className="block text-sm font-medium text-cloistr-text mb-1">
            Bunker URL
          </label>
          <input
            type="text"
            value={bunkerUrl}
            onChange={(e) => setBunkerUrl(e.target.value)}
            placeholder="bunker://..."
            className="w-full px-3 py-2 border border-cloistr-border rounded-lg focus:ring-2 focus:ring-cloistr-primary focus:border-cloistr-primary"
          />
          <p className="text-xs text-cloistr-text-muted mt-1">
            Format: bunker://pubkey?relay=wss://relay.example.com
          </p>
        </div>

        <button
          onClick={handleNip46Login}
          disabled={isLoading || !bunkerUrl}
          className="w-full px-4 py-3 bg-cloistr-primary text-white font-medium rounded-lg hover:bg-cloistr-primary-hover disabled:opacity-50 transition"
        >
          {isLoading ? 'Connecting to Bunker...' : 'Connect'}
        </button>

        <div className="mt-6 p-4 bg-cloistr-bg-elevated rounded-lg">
          <h4 className="font-medium text-sm mb-2">Benefits of nsecBunker:</h4>
          <ul className="text-sm text-cloistr-text-muted space-y-1">
            <li>• Your private key stays on the bunker device</li>
            <li>• Server can encrypt/decrypt emails on your behalf</li>
            <li>• Works across all your devices</li>
          </ul>
        </div>
      </div>
    </div>
    </div>
  )
}
