import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { authAPI } from '../lib/api'

export default function LoginPage() {
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const navigate = useNavigate()

  const handleLogin = async () => {
    setIsLoading(true)
    setError(null)

    try {
      // Stub: Actual implementation will:
      // 1. Request NIP-46 challenge
      // 2. Send to nsecbunker
      // 3. Wait for signature
      // 4. Verify and get session token
      // 5. Navigate to inbox

      // For now, just show the flow
      const challenge = await authAPI.startChallenge()
      console.log('Challenge:', challenge.data)

      setError('NIP-46 authentication not yet implemented')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed')
    } finally {
      setIsLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-100">
      <div className="bg-white rounded shadow-lg p-8 w-96">
        <h1 className="text-3xl font-bold text-center mb-6">Coldforge Mail</h1>

        <p className="text-gray-600 text-center mb-6">
          Login with your Nostr identity
        </p>

        {error && (
          <div className="mb-4 p-4 bg-red-100 border border-red-400 text-red-700 rounded">
            {error}
          </div>
        )}

        <button
          onClick={handleLogin}
          disabled={isLoading}
          className="w-full px-4 py-2 bg-blue-600 text-white font-medium rounded hover:bg-blue-700 disabled:opacity-50"
        >
          {isLoading ? 'Connecting...' : 'Login with Nostr'}
        </button>

        <p className="text-sm text-gray-600 text-center mt-4">
          Your nsecbunker will prompt you to approve this login
        </p>
      </div>
    </div>
  )
}
