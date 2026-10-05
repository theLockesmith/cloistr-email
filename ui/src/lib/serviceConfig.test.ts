import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// serviceConfig resolves at module load, from whatever /config.js wrote to
// window before the bundle ran. Each case sets that global, then imports a
// fresh copy of the module.
async function loadWith(runtime: Record<string, string> | undefined) {
  vi.stubGlobal('window', runtime ? { __CLOISTR_CONFIG__: runtime } : {})
  vi.resetModules()
  return import('./serviceConfig')
}

describe('serviceConfig', () => {
  beforeEach(() => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('resolves to the production signer when the container wrote no config', async () => {
    const { SIGNER_URL, serviceConfig } = await loadWith(undefined)
    expect(SIGNER_URL).toBe('https://signer.cloistr.xyz')
    expect(serviceConfig.environment).toBe('production')
  })

  it('uses the signer the container wrote, so one image can serve staging', async () => {
    const { SIGNER_URL, serviceConfig } = await loadWith({
      signerUrl: 'https://signer.staging.cloistr.xyz',
      environment: 'staging',
    })
    expect(SIGNER_URL).toBe('https://signer.staging.cloistr.xyz')
    expect(serviceConfig.environment).toBe('staging')
  })

  it('treats an empty substituted value as unset rather than as a host', async () => {
    const { SIGNER_URL } = await loadWith({ signerUrl: '', environment: '' })
    expect(SIGNER_URL).toBe('https://signer.cloistr.xyz')
  })
})
