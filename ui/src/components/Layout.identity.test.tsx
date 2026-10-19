// @vitest-environment jsdom
/**
 * Staging Mail called PRODUCTION https://cloistr.xyz/.well-known/nostr.json on
 * every signed-in page: the shared header's NIP-05 lookup had no way to learn
 * the deployment's domain, so it fell back to cloistr.xyz everywhere.
 *
 * This renders the real Layout (real shared Header and UserMenu) under the
 * /config.js that nginx.conf.template actually serves, with that config naming
 * a non-production domain, and asserts that nothing is requested from
 * cloistr.xyz. It reads the template rather than a hand-written config so the
 * test also fails if the container stops emitting the key.
 */
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { cleanup, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'

const PUBKEY = 'ab'.repeat(32)

vi.mock('@cloistr/ui/components', async (importOriginal) => {
  const real = await importOriginal<typeof import('@cloistr/ui/components')>()
  return {
    ...real,
    // Signed in, as on /inbox and /compose. Everything else is the real thing.
    useBackendAuth: () => ({
      isAuthenticated: () => true,
      user: { pubkey: PUBKEY },
      logout: () => {},
    }),
  }
})

/** The JS body nginx serves at /config.js, rendered with the given env. */
function renderedConfigJs(env: Record<string, string>): string {
  const template = readFileSync(resolve(__dirname, '../../nginx.conf.template'), 'utf8')
  const m = /location = \/config\.js \{[\s\S]*?return 200 '([^']*)';/.exec(template)
  if (!m) throw new Error('config.js location not found in nginx.conf.template')
  return m[1].replace(/\$\{(CLOISTR_[A-Z_]+)\}/g, (_, name: string) => env[name] ?? '')
}

const STAGING_ENV = {
  CLOISTR_RELAY_URL: 'wss://relay.example.org',
  CLOISTR_SIGNER_URL: 'https://signer.example.org',
  CLOISTR_BLOSSOM_URL: 'https://files.example.org',
  CLOISTR_DISCOVERY_URL: 'https://discover.example.org',
  CLOISTR_APP_URL: 'https://mail.example.org',
  CLOISTR_ENVIRONMENT: 'staging',
  CLOISTR_BASE_DOMAIN: 'example.org',
}

describe('Layout identity lookups follow the deployment', () => {
  let fetchMock: Mock<[RequestInfo | URL], Promise<Response>>

  beforeEach(() => {
    vi.resetModules()
    fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      const body = url.includes('/api/v1/users/me')
        ? { username: 'fraiyr', pubkey: PUBKEY }
        : url.includes('/api/v1/keys')
          ? []
          : { names: { fraiyr: PUBKEY } }
      return Promise.resolve(new Response(JSON.stringify(body), { status: 200 }))
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    delete (window as unknown as Record<string, unknown>).__CLOISTR_CONFIG__
  })

  it('serves baseDomain in /config.js', () => {
    expect(renderedConfigJs(STAGING_ENV)).toContain('"baseDomain":"example.org"')
  })

  it('never asks cloistr.xyz when config.js names another domain', async () => {
    // Same order as index.html: config.js runs before the bundle.
    new Function(renderedConfigJs(STAGING_ENV))()
    const { default: Layout } = await import('./Layout')
    const { AuthProvider } = await import('@cloistr/ui/auth')

    render(
      <AuthProvider>
        <MemoryRouter initialEntries={['/inbox']}>
          <Layout />
        </MemoryRouter>
      </AuthProvider>,
    )

    const urls = () => fetchMock.mock.calls.map((c) => String(c[0]))
    // Wait until the identity lookup has actually happened, so the absence of
    // a production request below means something.
    await waitFor(() =>
      expect(urls().some((u) => u.includes('/.well-known/nostr.json'))).toBe(true),
    )
    expect(urls(), JSON.stringify(urls())).toContain('https://example.org/.well-known/nostr.json?name=fraiyr')
    expect(urls().filter((u) => /^https:\/\/cloistr\.xyz\//.test(u))).toEqual([])
  })
})
