import { describe, it, expect } from 'vitest'
import { nip44, generateSecretKey, getPublicKey } from 'nostr-tools'
import { wrapProtectedHeaders, parseProtectedHeaders, ENCRYPTED_SUBJECT_PLACEHOLDER } from './protected-headers'
import { buildClientEncryptedSend } from './email-compose'

describe('protected headers', () => {
  it('round-trips the subject and body', () => {
    const p = parseProtectedHeaders(wrapProtectedHeaders('Q3 salaries', 'Hi,\n\nsee attached.\n'))
    expect(p.subject).toBe('Q3 salaries')
    expect(p.body).toBe('Hi,\n\nsee attached.\n')
  })

  it('leaves a body that merely starts with "Subject:" alone', () => {
    const text = 'Subject: not a header\n\nbody'
    expect(parseProtectedHeaders(text)).toEqual({ body: text })
  })

  it('leaves older messages without the block unchanged', () => {
    expect(parseProtectedHeaders('plain old body')).toEqual({ body: 'plain old body' })
  })

  it('folds a pasted multi-line subject so it cannot inject headers or end the block', () => {
    const p = parseProtectedHeaders(wrapProtectedHeaders('one\r\n\r\nX-Evil: 1', 'body'))
    expect(p.subject).toBe('one X-Evil: 1')
    expect(p.body).toBe('body')
  })
})

describe('client-mode send', () => {
  const sk = generateSecretKey()
  const rpk = getPublicKey(generateSecretKey())
  const encrypt = async (pk: string, pt: string) => nip44.encrypt(pt, nip44.getConversationKey(sk, pk))

  it('keeps the real subject out of the request and inside the ciphertext', async () => {
    const req = await buildClientEncryptedSend({ subject: 'Q3 salaries', body: 'numbers' }, rpk, encrypt)
    expect(req.subject).toBe(ENCRYPTED_SUBJECT_PLACEHOLDER)
    expect(JSON.stringify(req)).not.toContain('Q3 salaries')
    expect(req.body).toBeUndefined()
    const plaintext = nip44.decrypt(req.pre_encrypted_body, nip44.getConversationKey(sk, rpk))
    expect(parseProtectedHeaders(plaintext)).toEqual({ subject: 'Q3 salaries', body: 'numbers' })
  })
})
