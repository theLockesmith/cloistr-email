import { useState, useCallback } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { authAPI, type UserInfo } from './api'
import { getPublicKey, signEvent, hasNostrExtension, type NostrEvent } from './nostr'

export interface UseAuthReturn {
  user: UserInfo | null
  pubkey: string | null
  isAuthenticated: boolean
  isLoading: boolean
  error: Error | null
  loginWithExtension: () => Promise<void>
  loginWithBunker: (bunkerUrl: string) => Promise<void>
  logout: () => Promise<void>
}

export function useAuth(): UseAuthReturn {
  const queryClient = useQueryClient()
  const [token, setToken] = useState<string | null>(
    localStorage.getItem('session_token')
  )
  const [pubkey, setPubkey] = useState<string | null>(
    localStorage.getItem('user_pubkey')
  )

  // Fetch user info when we have a token
  const {
    data: user,
    isLoading,
    error,
  } = useQuery({
    queryKey: ['auth', 'session'],
    queryFn: async () => {
      if (!token) return null
      try {
        const response = await authAPI.getSession()
        return response.data
      } catch (err) {
        // Token might be invalid, clear it
        localStorage.removeItem('session_token')
        localStorage.removeItem('user_pubkey')
        setToken(null)
        setPubkey(null)
        throw err
      }
    },
    enabled: !!token,
    retry: false,
  })

  // Login with NIP-07 browser extension
  const loginWithExtension = useCallback(async () => {
    if (!hasNostrExtension()) {
      throw new Error('No Nostr extension found')
    }

    // Get public key from extension
    const userPubkey = await getPublicKey()

    // Create auth event
    const authEvent: NostrEvent = {
      kind: 27235, // NIP-98 HTTP Auth
      created_at: Math.floor(Date.now() / 1000),
      tags: [
        ['u', window.location.origin + '/api/v1/auth/nip07/verify'],
        ['method', 'POST'],
      ],
      content: '',
    }

    // Sign the event
    const signedEvent = await signEvent(authEvent)

    // Verify with server
    const response = await authAPI.verifyNIP07(JSON.stringify(signedEvent))

    // Store credentials
    localStorage.setItem('session_token', response.data.token)
    localStorage.setItem('user_pubkey', userPubkey)
    setToken(response.data.token)
    setPubkey(userPubkey)

    // Refresh user data
    queryClient.invalidateQueries({ queryKey: ['auth', 'session'] })
  }, [queryClient])

  // Login with NIP-46 bunker
  const loginWithBunker = useCallback(
    async (bunkerUrl: string) => {
      // Start NIP-46 authentication
      const challengeResponse = await authAPI.startNIP46(bunkerUrl)
      const { challenge_id } = challengeResponse.data

      // Connect to bunker and get session
      const sessionResponse = await authAPI.connectBunker(challenge_id)

      // Store credentials
      localStorage.setItem('session_token', sessionResponse.data.token)
      localStorage.setItem('user_pubkey', sessionResponse.data.user_id)
      setToken(sessionResponse.data.token)
      setPubkey(sessionResponse.data.user_id)

      // Refresh user data
      queryClient.invalidateQueries({ queryKey: ['auth', 'session'] })
    },
    [queryClient]
  )

  // Logout
  const logout = useCallback(async () => {
    try {
      await authAPI.logout()
    } catch {
      // Ignore logout errors
    }

    localStorage.removeItem('session_token')
    localStorage.removeItem('user_pubkey')
    setToken(null)
    setPubkey(null)
    queryClient.clear()
  }, [queryClient])

  return {
    user: user || null,
    pubkey,
    isAuthenticated: !!token && !!user,
    isLoading: !!token && isLoading,
    error: error as Error | null,
    loginWithExtension,
    loginWithBunker,
    logout,
  }
}
