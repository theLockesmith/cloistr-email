import axios, { AxiosError } from 'axios'
import type { EncryptionMode } from './nostr'

const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080'

// Create axios instance with base URL
export const api = axios.create({
  baseURL: `${API_URL}/api/v1`,
  headers: {
    'Content-Type': 'application/json',
  },
})

// V2 API for email operations (with encryption support)
export const apiV2 = axios.create({
  baseURL: `${API_URL}/api/v2`,
  headers: {
    'Content-Type': 'application/json',
  },
})

// Add token to requests if available
const addAuthToken = (config: any) => {
  const token = localStorage.getItem('session_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
}

api.interceptors.request.use(addAuthToken)
apiV2.interceptors.request.use(addAuthToken)

// Handle auth errors
const handleAuthError = (error: AxiosError) => {
  if (error.response?.status === 401) {
    localStorage.removeItem('session_token')
    localStorage.removeItem('user_pubkey')
    window.location.href = '/login'
  }
  return Promise.reject(error)
}

api.interceptors.response.use((response) => response, handleAuthError)
apiV2.interceptors.response.use((response) => response, handleAuthError)

// ============================================================================
// Types
// ============================================================================

export interface Email {
  id: string
  message_id?: string
  from: string
  to: string | string[]
  cc?: string[]
  subject: string
  body?: string
  html_body?: string
  encrypted_body?: string
  is_encrypted: boolean
  encryption_mode?: EncryptionMode
  requires_client_decryption?: boolean
  sender_pubkey?: string
  sender_npub?: string
  folder: string
  created_at: string
  read_at?: string
}

export interface EmailListResponse {
  emails: Email[]
  total: number
  page: number
  limit: number
}

export interface SendEmailRequest {
  to: string[]
  cc?: string[]
  bcc?: string[]
  subject: string
  body?: string
  html_body?: string
  encryption_mode: EncryptionMode
  pre_encrypted_body?: string
  recipient_pubkeys?: Record<string, string>
  in_reply_to?: string
  references?: string[]
}

export interface SendEmailResponse {
  status: string
  message_id?: string
  encryption_mode: EncryptionMode
  recipient_results?: {
    email: string
    success: boolean
    encrypted: boolean
    error?: string
  }[]
  error?: string
}

export interface Contact {
  id: string
  email: string
  name?: string
  npub?: string
  notes?: string
  organization?: string
  always_encrypt: boolean
  blocked: boolean
  created_at: string
}

export interface ContactListResponse {
  contacts: Contact[]
  total: number
}

export interface KeyDiscoveryResponse {
  email: string
  npub?: string
  pubkey?: string
  found: boolean
  source?: string
}

export interface UserInfo {
  npub: string
  pubkey: string
  email?: string
  has_nip46?: boolean
  preferred_encryption_mode?: EncryptionMode
}

export interface AuthChallengeResponse {
  challenge_id: string
  challenge: string
  bunker_pubkey?: string
  relay_url?: string
  expires_at: number
}

export interface AuthVerifyResponse {
  token: string
  user_id: string
  expires_at: number
}

// ============================================================================
// Email API (v2 - with encryption support)
// ============================================================================

export const emailAPI = {
  list: (params?: {
    page?: number
    limit?: number
    direction?: string
    status?: string
    folder?: string
    search?: string
  }) => apiV2.get<EmailListResponse>('/emails', { params }),

  get: (id: string) => apiV2.get<Email>(`/emails/${id}`),

  send: (data: SendEmailRequest) => apiV2.post<SendEmailResponse>('/emails', data),

  delete: (id: string) => apiV2.delete(`/emails/${id}`),

  // Legacy v1 endpoints for backward compatibility
  reply: (id: string, data: any) => api.post(`/emails/${id}/reply`, data),
}

// ============================================================================
// Contact API
// ============================================================================

export const contactAPI = {
  list: (limit = 50, offset = 0) =>
    api.get<ContactListResponse>('/contacts', { params: { limit, offset } }),

  get: (id: string) => api.get<Contact>(`/contacts/${id}`),

  add: (data: Partial<Contact>) => api.post<Contact>('/contacts', data),

  update: (id: string, data: Partial<Contact>) =>
    api.put<Contact>(`/contacts/${id}`, data),

  delete: (id: string) => api.delete(`/contacts/${id}`),

  search: (query: string) =>
    api.get<ContactListResponse>('/contacts', { params: { search: query } }),
}

// ============================================================================
// Key Discovery API
// ============================================================================

export const keyAPI = {
  discover: (email: string) =>
    api.get<KeyDiscoveryResponse>('/keys/discover', { params: { email } }),

  import: (data: { email: string; npub?: string; pubkey?: string }) =>
    api.post('/keys/import', data),

  getMyKey: () => api.get<UserInfo>('/keys/mine'),
}

// ============================================================================
// Auth API
// ============================================================================

export const authAPI = {
  // Start NIP-46 authentication
  startNIP46: (bunkerUrl: string) =>
    api.post<AuthChallengeResponse>('/auth/nip46/start', { bunker_url: bunkerUrl }),

  // Verify NIP-46 signature
  verifyNIP46: (challengeId: string, signedEvent: string) =>
    api.post<AuthVerifyResponse>('/auth/nip46/verify', {
      challenge_id: challengeId,
      signed_event: signedEvent,
    }),

  // Connect to bunker
  connectBunker: (challengeId: string) =>
    api.post<AuthVerifyResponse>('/auth/nip46/connect', {
      challenge_id: challengeId,
    }),

  // NIP-07 authentication (extension-based)
  // The server verifies a signed event from the client
  verifyNIP07: (signedEvent: string) =>
    api.post<AuthVerifyResponse>('/auth/nip07/verify', {
      signed_event: signedEvent,
    }),

  // Get current session
  getSession: () => api.get<UserInfo>('/auth/session'),

  // Logout
  logout: () => api.post('/auth/logout', {}),
}

// ============================================================================
// Unified Address API
// ============================================================================

export const addressAPI = {
  // Get user's unified address
  get: () => api.get<{
    npub: string
    email?: string
    local_part?: string
    display_name?: string
    has_address: boolean
    verified: boolean
  }>('/address'),

  // Register a unified address
  register: (localPart: string, displayName: string) =>
    api.post<{
      email: string
      local_part: string
      verified: boolean
    }>('/address/register', {
      local_part: localPart,
      display_name: displayName,
    }),

  // Check if a local part is available
  checkAvailability: (localPart: string) =>
    api.get<{ available: boolean }>('/address/check', {
      params: { local_part: localPart },
    }),
}

// ============================================================================
// Encryption Capability API
// ============================================================================

export const encryptionAPI = {
  // Get user's encryption capabilities
  getCapabilities: () =>
    api.get<{
      npub: string
      has_nip46: boolean
      preferred_mode: EncryptionMode
      can_server_encrypt: boolean
      can_server_decrypt: boolean
    }>('/encryption/capabilities'),

  // Set preferred encryption mode
  setPreferredMode: (mode: EncryptionMode) =>
    api.post('/encryption/preferred-mode', { mode }),
}
