import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { BackendAuthProvider, ThemeProvider, ToastProvider } from '@cloistr/ui/components'
import '@cloistr/ui/styles'
import App from './App'
import './index.css'

const queryClient = new QueryClient()

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <ToastProvider>
          <BackendAuthProvider config={{ apiBase: '/api/v1' }}>
            <BrowserRouter>
              <App />
            </BrowserRouter>
          </BackendAuthProvider>
        </ToastProvider>
      </QueryClientProvider>
    </ThemeProvider>
  </React.StrictMode>,
)
