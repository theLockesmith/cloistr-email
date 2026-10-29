// @vitest-environment jsdom
/**
 * MAIL-8: on first load at phone width (390px), the sidebar toggle button
 * (`.cloistr-sidebar-toggle`) showed aria-label "Close navigation" and
 * aria-expanded="true" while the drawer it controls (the in-app navigation)
 * was NOT shown. The button's accessible state must always match the
 * drawer's actual visibility:
 *
 *   closed drawer -> "Open navigation", aria-expanded="false"
 *   opening it    -> the navigation becomes visible, button flips to "Close
 *                     navigation" / aria-expanded="true"
 *   Escape / tap-away -> closes the drawer and flips the button back
 *
 * Root cause: @cloistr/ui's `AppShellToggle` (dist/components/AppShell.js)
 * computes `expanded: ctx.open || !ctx.collapsed`, mixing the mobile drawer
 * state (`ctx.open`) with the desktop icon-rail collapse state
 * (`ctx.collapsed`) via OR. `ctx.collapsed` defaults to `false` ("rail not
 * collapsed") whenever the app does not drive AppShell's `collapsed` prop,
 * so `!ctx.collapsed` is `true` on first paint regardless of viewport, which
 * makes the OR always `true` on mobile even though the drawer is closed.
 *
 * Layout.tsx already owns a collapsed/expanded rail preference for its own
 * MailNav (`COLLAPSE_KEY`), but never fed it to AppShell's `collapsed` /
 * `onCollapsedChange` props, so AppShell's internal rail-collapse state was
 * always at its uncontrolled default of "not collapsed" -- exactly the
 * state that flips the mobile toggle to "expanded" on first load.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const PUBKEY = 'ab'.repeat(32)

vi.mock('@cloistr/ui/components', async (importOriginal) => {
  const real = await importOriginal<typeof import('@cloistr/ui/components')>()
  return {
    ...real,
    // Signed in, as on /inbox. Everything else (AppShell, Header, Sidebar,
    // useIsMobile...) is the real, installed @cloistr/ui code -- this test
    // exercises the actual dependency, not a stand-in for it.
    useBackendAuth: () => ({
      isAuthenticated: () => true,
      user: { pubkey: PUBKEY },
      logout: () => {},
    }),
  }
})

/** Make the environment report a phone-width viewport. */
function mockPhoneViewport(width = 390) {
  Object.defineProperty(window, 'innerWidth', { writable: true, configurable: true, value: width })

  window.matchMedia = vi.fn().mockImplementation((query: string) => {
    const match = /min-width:\s*(\d+)px/.exec(query)
    const minWidth = match ? Number(match[1]) : 0
    return {
      matches: width >= minWidth,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }
  })
}

describe('mobile nav drawer toggle (MAIL-8)', () => {
  beforeEach(() => {
    vi.resetModules()
    mockPhoneViewport(390)
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response(JSON.stringify({}), { status: 200 }))),
    )
  })

  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    delete (window as unknown as Record<string, unknown>).__CLOISTR_CONFIG__
  })

  async function renderMailAtPhoneWidth() {
    const { default: Layout } = await import('./Layout')
    const { AuthProvider } = await import('@cloistr/ui/auth')
    return render(
      <QueryClientProvider client={new QueryClient()}>
        <AuthProvider>
          <MemoryRouter initialEntries={['/inbox']}>
            <Layout />
          </MemoryRouter>
        </AuthProvider>
      </QueryClientProvider>,
    )
  }

  it('starts closed: button says "Open navigation" and the drawer is hidden', async () => {
    await renderMailAtPhoneWidth()

    const toggle = await screen.findByRole('button', { name: 'Open navigation' })
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(document.querySelector('[aria-label="In-app navigation"]')).toBeNull()
  })

  it('opening the drawer flips the button and shows the navigation', async () => {
    await renderMailAtPhoneWidth()

    const toggle = await screen.findByRole('button', { name: 'Open navigation' })
    fireEvent.click(toggle)

    const opened = await screen.findByRole('button', { name: 'Close navigation' })
    expect(opened.getAttribute('aria-expanded')).toBe('true')
    expect(document.querySelector('[aria-label="In-app navigation"]')).not.toBeNull()
  })

  it('Escape closes the drawer and flips the button back', async () => {
    await renderMailAtPhoneWidth()

    fireEvent.click(await screen.findByRole('button', { name: 'Open navigation' }))
    await screen.findByRole('button', { name: 'Close navigation' })

    fireEvent.keyDown(document, { key: 'Escape' })

    const toggle = await screen.findByRole('button', { name: 'Open navigation' })
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(document.querySelector('[aria-label="In-app navigation"]')).toBeNull()
  })

  it('tapping away (the scrim) closes the drawer and flips the button back', async () => {
    await renderMailAtPhoneWidth()

    fireEvent.click(await screen.findByRole('button', { name: 'Open navigation' }))
    await screen.findByRole('button', { name: 'Close navigation' })

    const scrim = await screen.findByTestId('appshell-scrim')
    fireEvent.click(scrim)

    const toggle = await screen.findByRole('button', { name: 'Open navigation' })
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(document.querySelector('[aria-label="In-app navigation"]')).toBeNull()
  })
})
