import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { authAPI } from '../lib/api'
import {
  waitForNostrExtension,
  getPublicKey,
  signEvent,
  hasNip44Support,
  type NostrEvent,
} from '../lib/nostr'

type AuthMethod = 'nip07' | 'nip46' | null

export default function LoginPage() {
  const [authMethod, setAuthMethod] = useState<AuthMethod>(null)
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [bunkerUrl, setBunkerUrl] = useState('')
  const [hasExtension, setHasExtension] = useState(false)
  const [extensionHasNip44, setExtensionHasNip44] = useState(false)
  const navigate = useNavigate()

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

  // Handle NIP-07 login (browser extension)
  const handleNip07Login = async () => {
    setIsLoading(true)
    setError(null)

    try {
      // Get public key from extension
      const pubkey = await getPublicKey()
      console.log('Got pubkey from extension:', pubkey)

      // Create auth event for the server to verify
      const authEvent: NostrEvent = {
        kind: 27235, // NIP-98 HTTP Auth
        created_at: Math.floor(Date.now() / 1000),
        tags: [
          ['u', window.location.origin + '/api/v1/auth/nip07/verify'],
          ['method', 'POST'],
        ],
        content: '',
      }

      // Sign the event using the extension
      const signedEvent = await signEvent(authEvent)
      console.log('Signed event:', signedEvent)

      // Send to server for verification
      const response = await authAPI.verifyNIP07(JSON.stringify(signedEvent))

      // Store session
      localStorage.setItem('session_token', response.data.token)
      localStorage.setItem('user_pubkey', pubkey)

      // Navigate to inbox
      navigate('/inbox')
    } catch (err) {
      console.error('NIP-07 login error:', err)
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setIsLoading(false)
    }
  }

  // Handle NIP-46 login (nsecbunker)
  const handleNip46Login = async () => {
    if (!bunkerUrl) {
      setError('Please enter your bunker URL')
      return
    }

    setIsLoading(true)
    setError(null)

    try {
      // Start NIP-46 authentication
      const challengeResponse = await authAPI.startNIP46(bunkerUrl)
      const { challenge_id } = challengeResponse.data

      console.log('Challenge created:', challenge_id)

      // Connect to bunker and get session
      const sessionResponse = await authAPI.connectBunker(challenge_id)

      // Store session
      localStorage.setItem('session_token', sessionResponse.data.token)
      localStorage.setItem('user_pubkey', sessionResponse.data.user_id)

      // Navigate to inbox
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
      <div className="min-h-screen flex items-center justify-center bg-gray-100">
        <div className="bg-white rounded-lg shadow-lg p-8 w-[420px]">
          <h1 className="text-3xl font-bold text-center mb-2">Coldforge Mail</h1>
          <p className="text-gray-600 text-center mb-8">
            Secure email with Nostr identity
          </p>

          <div className="space-y-4">
            {/* NIP-07 Browser Extension */}
            <button
              onClick={() => hasExtension ? setAuthMethod('nip07') : undefined}
              disabled={!hasExtension}
              className={`w-full p-4 border-2 rounded-lg text-left transition ${
                hasExtension
                  ? 'border-blue-500 hover:bg-blue-50 cursor-pointer'
                  : 'border-gray-200 bg-gray-50 cursor-not-allowed opacity-60'
              }`}
            >
              <div className="flex items-center justify-between">
                <div>
                  <h3 className="font-semibold text-lg">Browser Extension</h3>
                  <p className="text-sm text-gray-600">
                    {hasExtension
                      ? 'Use nos2x, Alby, or another NIP-07 extension'
                      : 'No extension detected'}
                  </p>
                </div>
                {hasExtension && (
                  <div className="flex flex-col items-end">
                    <span className="text-green-600 text-sm font-medium">Available</span>
                    {extensionHasNip44 && (
                      <span className="text-xs text-gray-500">NIP-44 supported</span>
                    )}
                  </div>
                )}
              </div>
            </button>

            {/* NIP-46 Bunker */}
            <button
              onClick={() => setAuthMethod('nip46')}
              className="w-full p-4 border-2 border-gray-300 rounded-lg text-left hover:bg-gray-50 transition"
            >
              <div>
                <h3 className="font-semibold text-lg">nsecBunker</h3>
                <p className="text-sm text-gray-600">
                  Connect to a remote signer for enhanced security
                </p>
              </div>
            </button>
          </div>

          <p className="text-xs text-gray-500 text-center mt-6">
            Your private key never leaves your device or bunker
          </p>
        </div>
      </div>
    )
  }

  // NIP-07 login screen
  if (authMethod === 'nip07') {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-100">
        <div className="bg-white rounded-lg shadow-lg p-8 w-[420px]">
          <button
            onClick={() => setAuthMethod(null)}
            className="text-gray-600 hover:text-gray-900 mb-4"
          >
            ← Back
          </button>

          <h1 className="text-2xl font-bold mb-2">Browser Extension Login</h1>
          <p className="text-gray-600 mb-6">
            Click the button below to sign in. Your extension will prompt you to approve.
          </p>

          {error && (
            <div className="mb-4 p-4 bg-red-100 border border-red-400 text-red-700 rounded">
              {error}
            </div>
          )}

          <button
            onClick={handleNip07Login}
            disabled={isLoading}
            className="w-full px-4 py-3 bg-blue-600 text-white font-medium rounded-lg hover:bg-blue-700 disabled:opacity-50 transition"
          >
            {isLoading ? 'Connecting...' : 'Sign in with Extension'}
          </button>

          <div className="mt-6 p-4 bg-gray-50 rounded-lg">
            <h4 className="font-medium text-sm mb-2">What happens:</h4>
            <ul className="text-sm text-gray-600 space-y-1">
              <li>1. Your extension will request access to your public key</li>
              <li>2. You'll be asked to sign a login message</li>
              <li>3. The server verifies your signature</li>
            </ul>
          </div>
        </div>
      </div>
    )
  }

  // NIP-46 login screen
  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-100">
      <div className="bg-white rounded-lg shadow-lg p-8 w-[420px]">
        <button
          onClick={() => setAuthMethod(null)}
          className="text-gray-600 hover:text-gray-900 mb-4"
        >
          ← Back
        </button>

        <h1 className="text-2xl font-bold mb-2">nsecBunker Login</h1>
        <p className="text-gray-600 mb-6">
          Enter your bunker connection URL to sign in securely.
        </p>

        {error && (
          <div className="mb-4 p-4 bg-red-100 border border-red-400 text-red-700 rounded">
            {error}
          </div>
        )}

        <div className="mb-4">
          <label className="block text-sm font-medium text-gray-700 mb-1">
            Bunker URL
          </label>
          <input
            type="text"
            value={bunkerUrl}
            onChange={(e) => setBunkerUrl(e.target.value)}
            placeholder="bunker://..."
            className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
          />
          <p className="text-xs text-gray-500 mt-1">
            Format: bunker://pubkey?relay=wss://relay.example.com
          </p>
        </div>

        <button
          onClick={handleNip46Login}
          disabled={isLoading || !bunkerUrl}
          className="w-full px-4 py-3 bg-blue-600 text-white font-medium rounded-lg hover:bg-blue-700 disabled:opacity-50 transition"
        >
          {isLoading ? 'Connecting to Bunker...' : 'Connect'}
        </button>

        <div className="mt-6 p-4 bg-gray-50 rounded-lg">
          <h4 className="font-medium text-sm mb-2">Benefits of nsecBunker:</h4>
          <ul className="text-sm text-gray-600 space-y-1">
            <li>• Your private key stays on the bunker device</li>
            <li>• Server can encrypt/decrypt emails on your behalf</li>
            <li>• Works across all your devices</li>
          </ul>
        </div>
      </div>
    </div>
  )
}
