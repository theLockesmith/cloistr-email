import { describe, it, expect, beforeEach, vi } from 'vitest'
import {
  composeDraftKey,
  saveDraft,
  loadDraft,
  clearDraft,
  hasDraftContent,
  type ComposeDraft,
} from './draft-autosave'

// ---------------------------------------------------------------------------
// localStorage mock
// localStorage does not exist in the node test environment. Stub it with an
// in-memory Map so the draft utilities can be tested without jsdom.
// ---------------------------------------------------------------------------

function makeLocalStorageMock() {
  const store = new Map<string, string>()
  return {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => { store.set(key, value) },
    removeItem: (key: string) => { store.delete(key) },
    clear: () => { store.clear() },
    get length() { return store.size },
    key: (index: number) => [...store.keys()][index] ?? null,
  }
}

const localStorageMock = makeLocalStorageMock()
vi.stubGlobal('localStorage', localStorageMock)

const DRAFT_FIXTURE: ComposeDraft = {
  to: 'bob@example.com',
  cc: '',
  subject: 'Hello',
  body: 'Draft body text',
  encryptionMode: 'none',
  showCc: false,
  savedAt: 1724188800000,
}

beforeEach(() => {
  localStorageMock.clear()
})

// ---------------------------------------------------------------------------
// composeDraftKey
// ---------------------------------------------------------------------------

describe('composeDraftKey', () => {
  it('returns the generic compose key when no reply/forward context', () => {
    expect(composeDraftKey({})).toBe('email_draft_compose')
  })

  it('returns the generic compose key when both are null', () => {
    expect(composeDraftKey({ replyId: null, forwardId: null })).toBe('email_draft_compose')
  })

  it('returns a reply-scoped key for a replyId', () => {
    expect(composeDraftKey({ replyId: 'abc123' })).toBe('email_draft_reply_abc123')
  })

  it('returns a forward-scoped key for a forwardId', () => {
    expect(composeDraftKey({ forwardId: 'xyz789' })).toBe('email_draft_forward_xyz789')
  })

  it('reply takes priority over forward when both are set', () => {
    expect(composeDraftKey({ replyId: 'r1', forwardId: 'f1' })).toBe('email_draft_reply_r1')
  })

  it('different message IDs produce different keys', () => {
    const a = composeDraftKey({ replyId: 'id-a' })
    const b = composeDraftKey({ replyId: 'id-b' })
    expect(a).not.toBe(b)
  })

  it('reply and forward keys for the same ID differ from each other', () => {
    const r = composeDraftKey({ replyId: 'id1' })
    const f = composeDraftKey({ forwardId: 'id1' })
    expect(r).not.toBe(f)
  })
})

// ---------------------------------------------------------------------------
// saveDraft / loadDraft round-trip
// ---------------------------------------------------------------------------

describe('saveDraft and loadDraft', () => {
  it('round-trips a complete draft', () => {
    const key = 'email_draft_compose'
    saveDraft(key, DRAFT_FIXTURE)
    expect(loadDraft(key)).toEqual(DRAFT_FIXTURE)
  })

  it('preserves cc, encryption mode, and showCc', () => {
    const draft: ComposeDraft = {
      ...DRAFT_FIXTURE,
      cc: 'cc@example.com',
      encryptionMode: 'client',
      showCc: true,
    }
    saveDraft('email_draft_compose', draft)
    const loaded = loadDraft('email_draft_compose')
    expect(loaded?.cc).toBe('cc@example.com')
    expect(loaded?.encryptionMode).toBe('client')
    expect(loaded?.showCc).toBe(true)
  })

  it('loading from a missing key returns null', () => {
    expect(loadDraft('email_draft_compose')).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// loadDraft — validation / malformed input
// ---------------------------------------------------------------------------

describe('loadDraft validation', () => {
  it('returns null for malformed JSON', () => {
    localStorageMock.setItem('email_draft_compose', '{bad json}')
    expect(loadDraft('email_draft_compose')).toBeNull()
  })

  it('returns null when version does not match', () => {
    localStorageMock.setItem(
      'email_draft_compose',
      JSON.stringify({ v: 99, draft: DRAFT_FIXTURE }),
    )
    expect(loadDraft('email_draft_compose')).toBeNull()
  })

  it('returns null when `to` field is missing', () => {
    const { to: _to, ...rest } = DRAFT_FIXTURE
    localStorageMock.setItem(
      'email_draft_compose',
      JSON.stringify({ v: 1, draft: rest }),
    )
    expect(loadDraft('email_draft_compose')).toBeNull()
  })

  it('returns null when savedAt is not a number', () => {
    localStorageMock.setItem(
      'email_draft_compose',
      JSON.stringify({ v: 1, draft: { ...DRAFT_FIXTURE, savedAt: 'yesterday' } }),
    )
    expect(loadDraft('email_draft_compose')).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// clearDraft
// ---------------------------------------------------------------------------

describe('clearDraft', () => {
  it('removes a saved draft so loadDraft returns null', () => {
    const key = 'email_draft_compose'
    saveDraft(key, DRAFT_FIXTURE)
    clearDraft(key)
    expect(loadDraft(key)).toBeNull()
  })

  it('does not throw when clearing a key that does not exist', () => {
    expect(() => clearDraft('nonexistent_key')).not.toThrow()
  })
})

// ---------------------------------------------------------------------------
// hasDraftContent
// ---------------------------------------------------------------------------

describe('hasDraftContent', () => {
  it('returns true when `to` is non-empty', () => {
    expect(hasDraftContent({ ...DRAFT_FIXTURE, to: 'a@b.com', subject: '', body: '' })).toBe(true)
  })

  it('returns true when `subject` is non-empty', () => {
    expect(hasDraftContent({ ...DRAFT_FIXTURE, to: '', subject: 'Hello', body: '' })).toBe(true)
  })

  it('returns true when `body` is non-empty', () => {
    expect(hasDraftContent({ ...DRAFT_FIXTURE, to: '', subject: '', body: 'Hi there' })).toBe(true)
  })

  it('returns false when all meaningful fields are empty or whitespace', () => {
    expect(hasDraftContent({ ...DRAFT_FIXTURE, to: '', subject: '', body: '   ' })).toBe(false)
  })

  it('returns false for a fully empty draft', () => {
    expect(
      hasDraftContent({
        to: '',
        cc: '',
        subject: '',
        body: '',
        encryptionMode: 'none',
        showCc: false,
        savedAt: 0,
      }),
    ).toBe(false)
  })
})
