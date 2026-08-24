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

// Add token to requests if available.
//
// READS access_token, NOT session_token.
//
// BackendAuthProvider's token refresh (POST /api/v1/auth/refresh) rotates the
// session: the backend atomically deletes the old Redis session and issues a
// new token. The refresh callback in @cloistr/ui only updates `access_token`
// (and `token_expiry`) in localStorage — it has no knowledge of the email
// app's `session_token` alias.
//
// If this interceptor reads `session_token`, the first refresh invalidates the
// old token in Redis while `session_token` still holds the stale value. Every
// subsequent v2 API call sends the dead token and gets 401 "invalid session
// token", while `token-info` (which BackendAuthProvider calls with the fresh
// `access_token`) returns 200 — causing the login-inbox redirect loop and the
// "Error loading emails" error on mobile.
//
// Reading `access_token` directly means the interceptor always tracks whatever
// BackendAuthProvider has confirmed as the live session token.
const addAuthToken = (config: any) => {
  const token = localStorage.getItem('access_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
}

api.interceptors.request.use(addAuthToken)
apiV2.interceptors.request.use(addAuthToken)

// Handle auth errors
//
// SIGNER-RESILIENCE NOTE (2026-08-23):
//
// DO NOT clear localStorage here. The old code cleared all four auth keys on
// any 401, which caused two separate problems:
//
//   1. A relay hiccup could prevent useActiveKeyReScope from refreshing the
//      JWT. The next API call would then 401, and this handler would wipe the
//      session — logging the user out for a transient network blip.
//
//   2. Partial clears (the previous version) left access_token intact but
//      removed session_token, causing BackendAuthProvider to think the session
//      was valid while every API call immediately 401'd again (the redirect
//      loop the loop-breaker in App.tsx was added to catch).
//
// BackendAuthProvider.validateToken() now owns cleanup. When a 401 arrives
// here we redirect to /login without touching localStorage. On remount,
// validateToken() runs:
//   - If access_token is expired by timestamp -> clearAuth() removes it + resets state
//   - If access_token is present but backend rejects it -> clearAuth() does the same
//
// In both cases the user ends up on /login with a clean state.
// The loop-breaker in App.tsx catches any oscillation if validateToken keeps
// failing (>3 redirects in 1.5s) and clears everything as a last resort.
const handleAuthError = (error: AxiosError) => {
  if (error.response?.status === 401) {
    window.location.replace('/login')
  }
  return Promise.reject(error)
}

api.interceptors.response.use((response) => response, handleAuthError)
apiV2.interceptors.response.use((response) => response, handleAuthError)

// ============================================================================
// Types
// ============================================================================

export interface Attachment {
  attachment_id: string
  filename: string
  content_type?: string
  data_base64?: string       // set when fetched individually
  ciphertext?: string        // set when client-side encrypted
  requires_client_decryption?: boolean
}

export interface Email {
  id: string
  message_id?: string
  in_reply_to?: string
  references?: string        // space-separated RFC 2822 References value
  from: string
  to: string | string[]
  cc?: string | string[]
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
  labels?: string[]
  is_starred?: boolean
  has_attachments?: boolean
  attachments?: Attachment[]  // populated in detail view
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

export interface AttachmentRequest {
  filename: string
  content_type?: string
  data_base64: string  // standard base64-encoded bytes
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
  attachments?: AttachmentRequest[]
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

export interface EmailListParams {
  page?: number
  limit?: number
  direction?: string
  status?: string
  folder?: string
  search?: string
  from?: string
  to?: string
  label?: string    // filter by a single label value
  has_attachment?: boolean
  before?: string   // YYYY-MM-DD or RFC3339
  after?: string    // YYYY-MM-DD or RFC3339
  starred?: boolean
  unread?: boolean
  in_reply_to?: string
}

export const emailAPI = {
  list: (params?: EmailListParams) => apiV2.get<EmailListResponse>('/email', { params }),

  get: (id: string) => apiV2.get<Email>(`/email/${id}`),

  // Backend v2 send route is POST /api/v2/email/send (prefix is /email,
  // singular). Posting to /emails hit no route → 404.
  send: (data: SendEmailRequest) => apiV2.post<SendEmailResponse>('/email/send', data),

  delete: (id: string) => apiV2.delete(`/email/${id}`),

  archive: (id: string) => apiV2.patch(`/email/${id}/archive`, {}),

  markRead: (id: string) => apiV2.patch(`/email/${id}/read`, {}),

  markUnread: (id: string) => apiV2.patch(`/email/${id}/unread`, {}),

  star: (id: string, starred: boolean) => apiV2.patch(`/email/${id}/star`, { starred }),

  move: (id: string, folder: string) => apiV2.patch(`/email/${id}/move`, { folder }),

  addLabel: (id: string, label: string) => apiV2.post(`/email/${id}/labels`, { label }),

  removeLabel: (id: string, label: string) => apiV2.delete(`/email/${id}/labels`, { data: { label } }),

  getAttachment: (emailId: string, attachmentId: string) =>
    apiV2.get<Attachment>(`/email/${emailId}/attachments/${attachmentId}`),

  bulk: (ids: string[], action: string, folder?: string) =>
    apiV2.post('/email/bulk', { ids, action, folder }),

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
