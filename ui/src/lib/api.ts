import axios, { AxiosError } from 'axios'

const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080'

// Create axios instance with base URL
export const api = axios.create({
  baseURL: `${API_URL}/api/v1`,
  headers: {
    'Content-Type': 'application/json',
  },
})

// Add token to requests if available
api.interceptors.request.use((config) => {
  const token = localStorage.getItem('session_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// Handle auth errors
api.interceptors.response.use(
  (response) => response,
  (error: AxiosError) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('session_token')
      window.location.href = '/login'
    }
    return Promise.reject(error)
  }
)

// Email API
export const emailAPI = {
  list: (limit = 50, offset = 0) =>
    api.get('/emails', { params: { limit, offset } }),
  get: (id: string) => api.get(`/emails/${id}`),
  send: (data: any) => api.post('/emails', data),
  reply: (id: string, data: any) => api.post(`/emails/${id}/reply`, data),
  delete: (id: string) => api.delete(`/emails/${id}`),
}

// Contact API
export const contactAPI = {
  list: (limit = 50, offset = 0) =>
    api.get('/contacts', { params: { limit, offset } }),
  get: (id: string) => api.get(`/contacts/${id}`),
  add: (data: any) => api.post('/contacts', data),
  delete: (id: string) => api.delete(`/contacts/${id}`),
}

// Key Discovery API
export const keyAPI = {
  discover: (email: string) =>
    api.get('/keys/discover', { params: { email } }),
  import: (data: any) => api.post('/keys/import', data),
  getMyKey: () => api.get('/keys/mine'),
}

// Auth API
export const authAPI = {
  startChallenge: () => api.post('/auth/nip46/challenge', {}),
  verifyChallenge: (data: any) => api.post('/auth/nip46/verify', data),
  logout: () => api.post('/auth/logout', {}),
}
