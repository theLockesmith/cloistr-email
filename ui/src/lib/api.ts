import axios, { AxiosError } from 'axios'
import type { EncryptionMode } from './nostr'

// Origin prefix for the API. In production this is empty (same-origin: nginx
// proxies /api → backend), so baseURL becomes /api/v1. Use ?? not || so an
// intentionally-empty value is honored (|| would fall through to localhost and,
// combined with a VITE_API_URL of "/api", produced the /api/api/... double
// prefix that 404'd every call). Dev can set VITE_API_URL=http://localhost:8080.
const API_URL = import.meta.env.VITE_API_URL ?? ''

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
    // Clear ALL auth keys. Previously only session_token and user_pubkey were
    // removed, leaving access_token and token_expiry intact. On the next page
    // load BackendAuthProvider.validateToken() found a valid access_token and
    // set user+token in memory (isAuthenticated()=true), but every API call
    // immediately 401'd again because session_token was still missing — creating
    // the login redirect loop the operator reported.
    localStorage.removeItem('access_token')
    localStorage.removeItem('token_expiry')
    localStorage.removeItem('user_pubkey')
    localStorage.removeItem('session_token')
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
  // Nostr signature verification (RFC-002)
  nostr_verified?: boolean
  nostr_verified_at?: string
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
  }) => apiV2.get<EmailListResponse>('/email', { params }),

  get: (id: string) => apiV2.get<Email>(`/email/${id}`),

  // Backend v2 send route is POST /api/v2/email/send (prefix is /email,
  // singular). Posting to /emails hit no route → 404.
  send: (data: SendEmailRequest) => apiV2.post<SendEmailResponse>('/email/send', data),

  delete: (id: string) => apiV2.delete(`/email/${id}`),

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

// NOTE: the former `authAPI` export was REMOVED 2026-08-17. It defined five
// endpoints — /auth/nip46/start, /auth/nip46/verify, /auth/nip46/connect,
// /auth/nip07/verify, /auth/session — of which the backend registers only
// /auth/nip46/verify. It had no callers anywhere in the app: real auth goes
// through BackendAuthProvider (/auth/challenge, /auth/verify, /auth/token-info)
// in @cloistr/ui. Dead code that reads like the auth path is worse than none:
// it was the first place investigation landed when SSO appeared to fail
// silently, and it cost a wrong root cause before the call graph was checked.
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
