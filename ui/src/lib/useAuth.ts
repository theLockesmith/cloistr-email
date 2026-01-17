import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from './api'

export interface User {
  npub: string
  email: string
}

export interface UseAuthReturn {
  user: User | null
  isAuthenticated: boolean
  isLoading: boolean
  error: Error | null
  login: () => Promise<void>
  logout: () => Promise<void>
}

export function useAuth(): UseAuthReturn {
  const [token, setToken] = useState<string | null>(
    localStorage.getItem('session_token')
  )

  const { data: user, isLoading, error } = useQuery({
    queryKey: ['auth', 'user'],
    queryFn: async () => {
      if (!token) return null
      // Fetch user info from API
      const response = await api.get('/keys/mine')
      return response.data
    },
    enabled: !!token,
  })

  const login = async () => {
    // Stub: Actual implementation will:
    // 1. Create NIP-46 challenge via API
    // 2. Send challenge to nsecbunker
    // 3. Get signature back
    // 4. Verify signature and get session token
    // 5. Store token in localStorage
    // 6. Store in state
  }

  const logout = async () => {
    // Stub: Actual implementation will:
    // 1. Call API logout endpoint
    // 2. Clear localStorage
    // 3. Clear state
    setToken(null)
    localStorage.removeItem('session_token')
  }

  return {
    user: user || null,
    isAuthenticated: !!token && !!user,
    isLoading,
    error: error as Error | null,
    login,
    logout,
  }
}
