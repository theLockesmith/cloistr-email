/**
 * NIP-07 Browser Extension Integration
 *
 * This module provides integration with Nostr browser extensions (nos2x, Alby, etc.)
 * that implement NIP-07 for key management and signing.
 *
 * NIP-07 defines a window.nostr object with methods for:
 * - Getting the user's public key
 * - Signing events
 * - Encrypting/decrypting messages (NIP-04 and NIP-44)
 */

// Type declarations for window.nostr (NIP-07)
declare global {
  interface Window {
    nostr?: Nostr
  }
}

export interface NostrEvent {
  kind: number
  created_at: number
  tags: string[][]
  content: string
  pubkey?: string
  id?: string
  sig?: string
}

export interface Nostr {
  getPublicKey(): Promise<string>
  signEvent(event: NostrEvent): Promise<NostrEvent>
  getRelays?(): Promise<Record<string, { read: boolean; write: boolean }>>

  // NIP-04 encryption (legacy)
  nip04?: {
    encrypt(pubkey: string, plaintext: string): Promise<string>
    decrypt(pubkey: string, ciphertext: string): Promise<string>
  }

  // NIP-44 encryption (preferred)
  nip44?: {
    encrypt(pubkey: string, plaintext: string): Promise<string>
    decrypt(pubkey: string, ciphertext: string): Promise<string>
  }
}

/**
 * Check if NIP-07 extension is available
 */
export function hasNostrExtension(): boolean {
  return typeof window !== 'undefined' && !!window.nostr
}

/**
 * Wait for NIP-07 extension to be available (with timeout)
 */
export async function waitForNostrExtension(timeoutMs = 3000): Promise<boolean> {
  if (hasNostrExtension()) return true

  return new Promise((resolve) => {
    const startTime = Date.now()

    const checkInterval = setInterval(() => {
      if (hasNostrExtension()) {
        clearInterval(checkInterval)
        resolve(true)
      } else if (Date.now() - startTime > timeoutMs) {
        clearInterval(checkInterval)
        resolve(false)
      }
    }, 100)
  })
}

/**
 * Get the user's public key from the extension
 */
export async function getPublicKey(): Promise<string> {
  if (!window.nostr) {
    throw new Error('No Nostr extension found. Please install nos2x, Alby, or another NIP-07 extension.')
  }

  try {
    return await window.nostr.getPublicKey()
  } catch (err) {
    throw new Error(`Failed to get public key: ${err instanceof Error ? err.message : 'Unknown error'}`)
  }
}

/**
 * Sign a Nostr event using the extension
 */
export async function signEvent(event: NostrEvent): Promise<NostrEvent> {
  if (!window.nostr) {
    throw new Error('No Nostr extension found')
  }

  try {
    return await window.nostr.signEvent(event)
  } catch (err) {
    throw new Error(`Failed to sign event: ${err instanceof Error ? err.message : 'Unknown error'}`)
  }
}

/**
 * Check if NIP-44 encryption is supported
 */
export function hasNip44Support(): boolean {
  return hasNostrExtension() && !!window.nostr?.nip44
}

/**
 * Check if NIP-04 encryption is supported (legacy)
 */
export function hasNip04Support(): boolean {
  return hasNostrExtension() && !!window.nostr?.nip04
}

/**
 * Encrypt a message using NIP-44 (preferred) or NIP-04 (fallback)
 */
export async function encrypt(recipientPubkey: string, plaintext: string): Promise<string> {
  if (!window.nostr) {
    throw new Error('No Nostr extension found')
  }

  // Prefer NIP-44
  if (window.nostr.nip44) {
    try {
      return await window.nostr.nip44.encrypt(recipientPubkey, plaintext)
    } catch (err) {
      throw new Error(`NIP-44 encryption failed: ${err instanceof Error ? err.message : 'Unknown error'}`)
    }
  }

  // Fallback to NIP-04
  if (window.nostr.nip04) {
    try {
      return await window.nostr.nip04.encrypt(recipientPubkey, plaintext)
    } catch (err) {
      throw new Error(`NIP-04 encryption failed: ${err instanceof Error ? err.message : 'Unknown error'}`)
    }
  }

  throw new Error('No encryption support found in Nostr extension')
}

/**
 * Decrypt a message using NIP-44 (preferred) or NIP-04 (fallback)
 */
export async function decrypt(senderPubkey: string, ciphertext: string): Promise<string> {
  if (!window.nostr) {
    throw new Error('No Nostr extension found')
  }

  // Prefer NIP-44
  if (window.nostr.nip44) {
    try {
      return await window.nostr.nip44.decrypt(senderPubkey, ciphertext)
    } catch (err) {
      // If NIP-44 fails, try NIP-04 (message might be encrypted with older method)
      if (window.nostr.nip04) {
        try {
          return await window.nostr.nip04.decrypt(senderPubkey, ciphertext)
        } catch {
          // NIP-04 also failed, throw original NIP-44 error
        }
      }
      throw new Error(`Decryption failed: ${err instanceof Error ? err.message : 'Unknown error'}`)
    }
  }

  // Fallback to NIP-04
  if (window.nostr.nip04) {
    try {
      return await window.nostr.nip04.decrypt(senderPubkey, ciphertext)
    } catch (err) {
      throw new Error(`NIP-04 decryption failed: ${err instanceof Error ? err.message : 'Unknown error'}`)
    }
  }

  throw new Error('No decryption support found in Nostr extension')
}

/**
 * Encryption mode for email sending
 */
export type EncryptionMode = 'none' | 'server' | 'client'

/**
 * Determine the best encryption mode based on available capabilities
 */
export function getAvailableEncryptionModes(): EncryptionMode[] {
  const modes: EncryptionMode[] = ['none']

  // Server-side encryption is always available if authenticated
  modes.push('server')

  // Client-side encryption requires NIP-07 extension with encryption support
  if (hasNip44Support() || hasNip04Support()) {
    modes.push('client')
  }

  return modes
}

/**
 * Get a human-readable description of an encryption mode
 */
export function getEncryptionModeDescription(mode: EncryptionMode): string {
  switch (mode) {
    case 'none':
      return 'No encryption - message sent in plaintext'
    case 'server':
      return 'Server-side encryption - encrypted using your bunker connection'
    case 'client':
      return 'Client-side encryption - encrypted locally, server never sees plaintext'
    default:
      return 'Unknown encryption mode'
  }
}

/**
 * Convert hex pubkey to npub format
 */
export function hexToNpub(hex: string): string {
  // Note: This is a simplified version. In production, use nostr-tools bech32 encoding
  return `npub1${hex.slice(0, 20)}...`
}

/**
 * Truncate a pubkey for display
 */
export function truncatePubkey(pubkey: string, length = 8): string {
  if (pubkey.length <= length * 2) return pubkey
  return `${pubkey.slice(0, length)}...${pubkey.slice(-length)}`
}
