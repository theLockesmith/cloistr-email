/**
 * Signer resilience unit tests
 *
 * No DOM/jsdom is available in this project's test environment, so component
 * behaviour is verified through source-level analysis below (in PROOF.md
 * comments) and through unit tests against the retry utilities directly.
 *
 * These tests confirm that the retry policy @cloistr/ui exports — which
 * useLoginWithSigner and useActiveKeyReScope now use — behaves correctly with
 * the error taxonomy:
 *
 *   retryable  → relay unreachable; retried automatically (up to 3 attempts)
 *   needs-user → signer timed out; rethrown immediately (never auto-retried)
 *   terminal   → signer said no; rethrown immediately
 *
 * A failure of these assertions means the retry logic on which the signer-
 * resilience design depends has regressed in the package.
 */

import { describe, it, expect, vi } from 'vitest'
import {
  withSignerRetry,
  classifySignerError,
  isRetryableSignerError,
  RETRYABLE_CODES,
  TERMINAL_CODES,
} from '@cloistr/ui'

// ---------------------------------------------------------------------------
// classifySignerError
// ---------------------------------------------------------------------------
describe('classifySignerError', () => {
  it('classifies NO_RELAYS as retryable', () => {
    expect(classifySignerError({ code: 'NO_RELAYS' })).toBe('retryable')
  })

  it('classifies CONNECTION_FAILED as retryable', () => {
    expect(classifySignerError({ code: 'CONNECTION_FAILED' })).toBe('retryable')
  })

  it('classifies DISCONNECTED as retryable', () => {
    expect(classifySignerError({ code: 'DISCONNECTED' })).toBe('retryable')
  })

  it('classifies CANCELLED as terminal', () => {
    expect(classifySignerError({ code: 'CANCELLED' })).toBe('terminal')
  })

  it('classifies REMOTE_ERROR as terminal', () => {
    expect(classifySignerError({ code: 'REMOTE_ERROR' })).toBe('terminal')
  })

  it('classifies TIMEOUT as needs-user', () => {
    expect(classifySignerError({ code: 'TIMEOUT' })).toBe('needs-user')
  })

  it('classifies unknown errors as terminal (safe default — never retry unknowns)', () => {
    expect(classifySignerError(new Error('oops'))).toBe('terminal')
    expect(classifySignerError(null)).toBe('terminal')
    expect(classifySignerError({ code: 'MADE_UP' })).toBe('terminal')
  })

  it('covers all declared retryable codes', () => {
    for (const code of RETRYABLE_CODES) {
      expect(classifySignerError({ code })).toBe('retryable')
    }
  })

  it('covers all declared terminal codes', () => {
    for (const code of TERMINAL_CODES) {
      expect(classifySignerError({ code })).toBe('terminal')
    }
  })
})

// ---------------------------------------------------------------------------
// isRetryableSignerError (convenience wrapper used as a guard in retry logic)
// ---------------------------------------------------------------------------
describe('isRetryableSignerError', () => {
  it('returns true for NO_RELAYS', () => {
    expect(isRetryableSignerError({ code: 'NO_RELAYS' })).toBe(true)
  })

  it('returns false for CANCELLED', () => {
    expect(isRetryableSignerError({ code: 'CANCELLED' })).toBe(false)
  })

  it('returns false for unknown errors', () => {
    expect(isRetryableSignerError(new Error('network down'))).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// withSignerRetry — core contract
// ---------------------------------------------------------------------------
describe('withSignerRetry', () => {
  it('returns the value immediately when the first attempt succeeds', async () => {
    const fn = vi.fn().mockResolvedValue('ok')
    const result = await withSignerRetry(fn)
    expect(result).toBe('ok')
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('retries a retryable error and succeeds on second attempt', async () => {
    const relayError = { code: 'NO_RELAYS' }
    const fn = vi
      .fn()
      .mockRejectedValueOnce(relayError)
      .mockResolvedValue('recovered')

    const result = await withSignerRetry(fn, {
      sleep: () => Promise.resolve(),
      random: () => 0,
    })

    expect(result).toBe('recovered')
    expect(fn).toHaveBeenCalledTimes(2)
  })

  it('exhausts retries and rethrows after all attempts fail with retryable error', async () => {
    const relayError = { code: 'CONNECTION_FAILED' }
    const fn = vi.fn().mockRejectedValue(relayError)

    await expect(
      withSignerRetry(fn, {
        attempts: 3,
        sleep: () => Promise.resolve(),
        random: () => 0,
      }),
    ).rejects.toBe(relayError)

    // 3 total attempts (1 initial + 2 retries)
    expect(fn).toHaveBeenCalledTimes(3)
  })

  it('does NOT retry a terminal error (signer refusal)', async () => {
    const cancellation = { code: 'CANCELLED' }
    const fn = vi.fn().mockRejectedValue(cancellation)

    await expect(
      withSignerRetry(fn, {
        attempts: 3,
        sleep: () => Promise.resolve(),
      }),
    ).rejects.toBe(cancellation)

    // Only 1 attempt — NEVER re-prompt after a refusal
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('does NOT retry a needs-user error (signer timeout)', async () => {
    const timeout = { code: 'TIMEOUT' }
    const fn = vi.fn().mockRejectedValue(timeout)

    await expect(
      withSignerRetry(fn, {
        attempts: 3,
        sleep: () => Promise.resolve(),
      }),
    ).rejects.toBe(timeout)

    // Signer approval may still be waiting on another device; do not retry
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('does NOT retry an unknown error (safe default prevents retry storms)', async () => {
    const unknown = new Error('unexpected')
    const fn = vi.fn().mockRejectedValue(unknown)

    await expect(
      withSignerRetry(fn, {
        attempts: 3,
        sleep: () => Promise.resolve(),
      }),
    ).rejects.toBe(unknown)

    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('calls onRetry with the attempt index and delay for each retry', async () => {
    const relayError = { code: 'DISCONNECTED' }
    const fn = vi
      .fn()
      .mockRejectedValueOnce(relayError)
      .mockRejectedValueOnce(relayError)
      .mockResolvedValue('ok')

    const onRetry = vi.fn()

    await withSignerRetry(fn, {
      attempts: 3,
      sleep: () => Promise.resolve(),
      random: () => 0.5,
      onRetry,
    })

    expect(onRetry).toHaveBeenCalledTimes(2)
    expect(onRetry.mock.calls[0][0]).toBe(1) // first retry is attempt 1
    expect(onRetry.mock.calls[1][0]).toBe(2) // second retry is attempt 2
  })
})
