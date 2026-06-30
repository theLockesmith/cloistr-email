package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/auth"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── in-memory mock SessionStore ───────────────────────────────────────────────

// mockStore is a mutex-guarded in-memory implementation of auth.SessionStore.
//
// ConsumeNIP46Challenge is fully atomic: the mutex is held for the entire
// read-then-delete, exactly mirroring Redis GETDEL. This means concurrent
// callers cannot both observe the challenge as present — exactly one caller
// receives the data, the rest receive (nil, nil).
type mockStore struct {
	mu         sync.Mutex
	challenges map[string]*auth.ChallengeData
	sessions   map[string]*auth.Session // keyed by session ID
	tokens     map[string]string        // bearer token → session ID
}

func newMockStore() *mockStore {
	return &mockStore{
		challenges: make(map[string]*auth.ChallengeData),
		sessions:   make(map[string]*auth.Session),
		tokens:     make(map[string]string),
	}
}

func (m *mockStore) SaveSession(_ context.Context, s *auth.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *s
	m.sessions[s.ID] = &cp
	m.tokens[s.Token] = s.ID
	return nil
}

func (m *mockStore) GetSession(_ context.Context, id string) (*auth.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, nil
}

func (m *mockStore) GetSessionByToken(_ context.Context, token string) (*auth.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.tokens[token]
	if !ok {
		return nil, nil
	}
	if s, ok := m.sessions[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, nil
}

func (m *mockStore) DeleteSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		delete(m.tokens, s.Token)
	}
	delete(m.sessions, id)
	return nil
}

func (m *mockStore) SetNIP46Challenge(_ context.Context, nonce string, data *auth.ChallengeData, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *data
	m.challenges[nonce] = &cp
	return nil
}

func (m *mockStore) GetNIP46Challenge(_ context.Context, nonce string) (*auth.ChallengeData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.challenges[nonce]; ok {
		cp := *d
		return &cp, nil
	}
	return nil, nil
}

func (m *mockStore) DeleteNIP46Challenge(_ context.Context, nonce string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.challenges, nonce)
	return nil
}

// ConsumeNIP46Challenge atomically reads-and-deletes the challenge under a
// single lock acquisition. Exactly one concurrent caller wins; the rest see nil.
func (m *mockStore) ConsumeNIP46Challenge(_ context.Context, nonce string) (*auth.ChallengeData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.challenges[nonce]
	if !ok {
		return nil, nil
	}
	cp := *d
	delete(m.challenges, nonce)
	return &cp, nil
}

// hasChallenge reports whether nonce is still present in the store.
// Used in tests to assert that a challenge was NOT consumed.
func (m *mockStore) hasChallenge(nonce string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.challenges[nonce]
	return ok
}

// ── test helpers ──────────────────────────────────────────────────────────────

// mustSeedChallenge stores a challenge/nonce pair directly in the mock.
func mustSeedChallenge(t *testing.T, store *mockStore, challenge, nonce string) {
	t.Helper()
	err := store.SetNIP46Challenge(context.Background(), nonce, &auth.ChallengeData{
		Challenge: challenge,
		CreatedAt: time.Now().Unix(),
	}, 5*time.Minute)
	require.NoError(t, err)
}

// buildSignedEvent constructs and signs a well-formed kind-27235 nostr.Event
// for VerifyChallenge. The tag and content both carry the same challenge value.
func buildSignedEvent(t *testing.T, privKeyHex, challenge, nonce string) nostr.Event {
	t.Helper()
	contentBytes, err := json.Marshal(csEventContent{Challenge: challenge, Nonce: nonce})
	require.NoError(t, err)
	ev := nostr.Event{
		Kind:      27235,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"challenge", challenge}},
		Content:   string(contentBytes),
	}
	require.NoError(t, ev.Sign(privKeyHex))
	return ev
}

// doVerifyChallenge wraps h.VerifyChallenge in an httptest round-trip.
func doVerifyChallenge(t *testing.T, h *Handler, ev nostr.Event) *httptest.ResponseRecorder {
	t.Helper()
	evJSON, err := json.Marshal(ev)
	require.NoError(t, err)
	body, err := json.Marshal(verifyChallengeRequest{SignedEvent: json.RawMessage(evJSON)})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.VerifyChallenge(w, req)
	return w
}

// ── test suite ────────────────────────────────────────────────────────────────

func TestClientSideAuth(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()

	// ── StartChallenge ────────────────────────────────────────────────────────

	t.Run("StartChallenge", func(t *testing.T) {
		t.Parallel()

		t.Run("returns_200_with_nonempty_challenge_and_nonce", func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			h := &Handler{sessions: store, logger: logger}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/challenge", nil)
			w := httptest.NewRecorder()
			h.StartChallenge(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			var resp startChallengeResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.NotEmpty(t, resp.Challenge, "challenge field must be non-empty")
			assert.NotEmpty(t, resp.Nonce, "nonce field must be non-empty")

			// The issued challenge must be retrievable from the store using the returned nonce.
			stored, err := store.GetNIP46Challenge(context.Background(), resp.Nonce)
			require.NoError(t, err)
			require.NotNil(t, stored, "challenge must be stored under the returned nonce")
			assert.Equal(t, resp.Challenge, stored.Challenge)
		})

		t.Run("successive_calls_produce_unique_challenge_and_nonce_values", func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			h := &Handler{sessions: store, logger: logger}

			challenges := make(map[string]struct{})
			nonces := make(map[string]struct{})
			for i := 0; i < 10; i++ {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/challenge", nil)
				w := httptest.NewRecorder()
				h.StartChallenge(w, req)
				require.Equal(t, http.StatusOK, w.Code)
				var resp startChallengeResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				_, dupC := challenges[resp.Challenge]
				_, dupN := nonces[resp.Nonce]
				assert.False(t, dupC, "duplicate challenge on iteration %d", i)
				assert.False(t, dupN, "duplicate nonce on iteration %d", i)
				challenges[resp.Challenge] = struct{}{}
				nonces[resp.Nonce] = struct{}{}
			}
		})
	})

	// ── VerifyChallenge ───────────────────────────────────────────────────────

	t.Run("VerifyChallenge", func(t *testing.T) {
		t.Parallel()

		// Case 1 — happy path: valid signed event → 200, token, correct pubkey.
		t.Run("1_valid_event_returns_200_with_token_and_signer_pubkey", func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			h := &Handler{sessions: store, logger: logger}

			privKey := nostr.GeneratePrivateKey()
			pubKey, err := nostr.GetPublicKey(privKey)
			require.NoError(t, err)

			challenge := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
			nonce := "c1d2e3f4a5b6c1d2e3f4a5b6"
			mustSeedChallenge(t, store, challenge, nonce)

			ev := buildSignedEvent(t, privKey, challenge, nonce)
			w := doVerifyChallenge(t, h, ev)

			require.Equal(t, http.StatusOK, w.Code)
			var resp csAuthResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.NotEmpty(t, resp.Token, "response must carry a session token")
			assert.Equal(t, pubKey, resp.Pubkey, "response pubkey must match the event signer")
			assert.Greater(t, resp.ExpiresAt, time.Now().Unix(), "expires_at must be in the future")
		})

		// Case 2 — tampered sig: handler must check signature BEFORE consuming the
		// challenge. Challenge must remain in the store after the rejected request.
		t.Run("2_bad_signature_returns_401_and_does_not_consume_challenge", func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			h := &Handler{sessions: store, logger: logger}

			privKey := nostr.GeneratePrivateKey()
			challenge := "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3"
			nonce := "d2e3f4a5b6c1d2e3f4a5b6c1"
			mustSeedChallenge(t, store, challenge, nonce)

			ev := buildSignedEvent(t, privKey, challenge, nonce)
			// Replace the valid schnorr sig with 64 zero-bytes (128 hex chars).
			ev.Sig = "0000000000000000000000000000000000000000000000000000000000000000" +
				"0000000000000000000000000000000000000000000000000000000000000000"

			w := doVerifyChallenge(t, h, ev)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
			// The signature check runs at step (a), before ConsumeNIP46Challenge at
			// step (d). A rejected signature must never drain the one-time challenge.
			assert.True(t, store.hasChallenge(nonce),
				"challenge must NOT be consumed when the event signature is invalid")
		})

		// Case 3 — content.challenge ≠ stored challenge. The handler consumes the
		// nonce (ConsumeNIP46Challenge runs before the mismatch check) and returns 401.
		t.Run("3_wrong_content_challenge_returns_401", func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			h := &Handler{sessions: store, logger: logger}

			privKey := nostr.GeneratePrivateKey()
			storedChallenge := "c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"
			nonce := "e3f4a5b6c1d2e3f4a5b6c1d2"
			mustSeedChallenge(t, store, storedChallenge, nonce)

			// Sign with a challenge value that does NOT match what is stored.
			differentChallenge := "1111111111111111111111111111111111111111111111111111111111111111"
			ev := buildSignedEvent(t, privKey, differentChallenge, nonce)

			w := doVerifyChallenge(t, h, ev)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})

		// Case 4 — ["challenge", tag] value ≠ content.challenge. Content matches the
		// stored challenge (to reach the tag check), but the tag carries a different value.
		t.Run("4_challenge_tag_mismatch_returns_401", func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			h := &Handler{sessions: store, logger: logger}

			privKey := nostr.GeneratePrivateKey()
			challenge := "d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5"
			nonce := "f4a5b6c1d2e3f4a5b6c1d2e3"
			mustSeedChallenge(t, store, challenge, nonce)

			// Content has the correct challenge; tag has a deliberately different value.
			contentBytes, err := json.Marshal(csEventContent{Challenge: challenge, Nonce: nonce})
			require.NoError(t, err)
			ev := nostr.Event{
				Kind:      27235,
				CreatedAt: nostr.Now(),
				Tags:      nostr.Tags{{"challenge", "2222222222222222222222222222222222222222222222222222222222222222"}},
				Content:   string(contentBytes),
			}
			require.NoError(t, ev.Sign(privKey))

			w := doVerifyChallenge(t, h, ev)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})

		// Case 5 — event.Kind ≠ 27235. Kind is checked at step (b), before the
		// challenge is consumed.
		t.Run("5_wrong_kind_returns_401", func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			h := &Handler{sessions: store, logger: logger}

			privKey := nostr.GeneratePrivateKey()
			challenge := "e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6"
			nonce := "a5b6c1d2e3f4a5b6c1d2e3f4"
			mustSeedChallenge(t, store, challenge, nonce)

			contentBytes, err := json.Marshal(csEventContent{Challenge: challenge, Nonce: nonce})
			require.NoError(t, err)
			ev := nostr.Event{
				Kind:      1, // must be 27235
				CreatedAt: nostr.Now(),
				Tags:      nostr.Tags{{"challenge", challenge}},
				Content:   string(contentBytes),
			}
			require.NoError(t, ev.Sign(privKey))

			w := doVerifyChallenge(t, h, ev)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})

		// Case 6 — event.CreatedAt is 10 minutes old, outside the ±5 min replay window.
		// The challenge is consumed before the timestamp check, so the 401 is still correct.
		t.Run("6_stale_created_at_returns_401", func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			h := &Handler{sessions: store, logger: logger}

			privKey := nostr.GeneratePrivateKey()
			challenge := "f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1"
			nonce := "b6c1d2e3f4a5b6c1d2e3f4a5"
			mustSeedChallenge(t, store, challenge, nonce)

			contentBytes, err := json.Marshal(csEventContent{Challenge: challenge, Nonce: nonce})
			require.NoError(t, err)
			// 10 minutes in the past — diff = 600 > 300 → replay guard fires.
			ev := nostr.Event{
				Kind:      27235,
				CreatedAt: nostr.Timestamp(time.Now().Add(-10 * time.Minute).Unix()),
				Tags:      nostr.Tags{{"challenge", challenge}},
				Content:   string(contentBytes),
			}
			require.NoError(t, ev.Sign(privKey))

			w := doVerifyChallenge(t, h, ev)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})

		// Case 7 — nonce was never stored (unknown/expired). ConsumeNIP46Challenge
		// returns nil → 401 with the canonical "challenge not found" message.
		t.Run("7_unknown_nonce_returns_401_with_correct_error_message", func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			h := &Handler{sessions: store, logger: logger}

			privKey := nostr.GeneratePrivateKey()
			challenge := "a2b3c4d5e6f7a2b3c4d5e6f7a2b3c4d5e6f7a2b3c4d5e6f7a2b3c4d5e6f7a2b3"
			nonce := "neverseednonce0123456789" // not stored
			// Do NOT call mustSeedChallenge here.

			ev := buildSignedEvent(t, privKey, challenge, nonce)
			w := doVerifyChallenge(t, h, ev)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Contains(t, w.Body.String(), "challenge not found, expired, or already used")
		})

		// Case 8 — replay: submit the same signed event twice.
		// First call succeeds (200); second call must be rejected (401) because the
		// one-time challenge was atomically consumed on the first call.
		t.Run("8_replay_second_submission_rejected", func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			h := &Handler{sessions: store, logger: logger}

			privKey := nostr.GeneratePrivateKey()
			challenge := "b3c4d5e6f7a2b3c4d5e6f7a2b3c4d5e6f7a2b3c4d5e6f7a2b3c4d5e6f7a2b3c4"
			nonce := "replaynonce012345678901"
			mustSeedChallenge(t, store, challenge, nonce)

			ev := buildSignedEvent(t, privKey, challenge, nonce)

			w1 := doVerifyChallenge(t, h, ev)
			require.Equal(t, http.StatusOK, w1.Code, "first submission must succeed")

			// Resubmit the identical event — challenge is gone, must be rejected.
			w2 := doVerifyChallenge(t, h, ev)
			assert.Equal(t, http.StatusUnauthorized, w2.Code, "replayed event must be rejected")
			assert.Contains(t, w2.Body.String(), "challenge not found, expired, or already used")
		})

		// Case 9 — THE race-condition regression test.
		//
		// 50 goroutines all POST the same valid signed event simultaneously.
		// Because ConsumeNIP46Challenge is atomic (read-and-delete under one lock),
		// exactly ONE goroutine must receive a 200; the remaining 49 must receive 401.
		// This test MUST pass under `go test -race` to prove the fix is TOCTOU-free.
		t.Run("9_concurrency_exactly_one_success_among_50_goroutines", func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			h := &Handler{sessions: store, logger: logger}

			privKey := nostr.GeneratePrivateKey()
			challenge := "c4d5e6f7a2b3c4d5e6f7a2b3c4d5e6f7a2b3c4d5e6f7a2b3c4d5e6f7a2b3c4d5"
			nonce := "concurrencynonce01234567"
			mustSeedChallenge(t, store, challenge, nonce)

			ev := buildSignedEvent(t, privKey, challenge, nonce)

			// Pre-build the request body once; each goroutine creates its own reader.
			evJSON, err := json.Marshal(ev)
			require.NoError(t, err)
			reqBody, err := json.Marshal(verifyChallengeRequest{SignedEvent: json.RawMessage(evJSON)})
			require.NoError(t, err)

			const N = 50
			var (
				wg        sync.WaitGroup
				successes int64
				failures  int64
			)

			// Start gate: hold all goroutines until they are all scheduled.
			ready := make(chan struct{})

			for i := 0; i < N; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-ready // all goroutines release simultaneously
					req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify",
						bytes.NewReader(reqBody))
					req.Header.Set("Content-Type", "application/json")
					rec := httptest.NewRecorder()
					h.VerifyChallenge(rec, req)
					if rec.Code == http.StatusOK {
						atomic.AddInt64(&successes, 1)
					} else {
						atomic.AddInt64(&failures, 1)
					}
				}()
			}

			close(ready) // release all goroutines at once
			wg.Wait()

			assert.Equal(t, int64(1), successes,
				"exactly 1 goroutine must win (got %d successes, %d failures out of %d)",
				successes, failures, N)
			assert.Equal(t, int64(N-1), failures,
				"remaining %d goroutines must receive 401 (got %d successes, %d failures)",
				N-1, successes, failures)
		})
	})

	// ── RefreshToken and TokenInfo ────────────────────────────────────────────
	//
	// Both handlers delegate token validation to h.auth.ValidateSession, which
	// requires a *auth.NIP46Handler. NewNIP46Handler performs only CPU-bound
	// crypto (ephemeral keypair derivation) at construction time — it does NOT
	// connect to any relay — so it is safe to construct here without external deps.

	t.Run("RefreshToken_and_TokenInfo", func(t *testing.T) {
		t.Parallel()

		store := newMockStore()
		nip46, err := auth.NewNIP46Handler("wss://relay.example.com", store, logger)
		if err != nil {
			t.Skipf("cannot construct NIP46Handler without external relay: %v", err)
		}
		h := &Handler{sessions: store, auth: nip46, logger: logger}

		// seedSession plants a ready-to-use session in the shared store.
		seedSession := func(t *testing.T, id, token string) *auth.Session {
			t.Helper()
			s := &auth.Session{
				ID:        id,
				UserID:    "pubkey-for-" + id,
				Token:     token,
				CreatedAt: time.Now(),
				ExpiresAt: time.Now().Add(24 * time.Hour),
			}
			require.NoError(t, store.SaveSession(context.Background(), s))
			return s
		}

		t.Run("TokenInfo_valid_token_returns_200_valid_true_and_pubkey", func(t *testing.T) {
			t.Parallel()
			sess := seedSession(t, "ti-valid-001", "ti-valid-token-001")

			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/token-info", nil)
			req.Header.Set("Authorization", "Bearer "+sess.Token)
			w := httptest.NewRecorder()
			h.TokenInfo(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			var resp csTokenInfoResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.True(t, resp.Valid)
			assert.Equal(t, sess.UserID, resp.Pubkey)
			assert.Greater(t, resp.ExpiresAt, time.Now().Unix())
		})

		t.Run("TokenInfo_unknown_token_returns_401_valid_false", func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/token-info", nil)
			req.Header.Set("Authorization", "Bearer bogus-unknown-token-xyz")
			w := httptest.NewRecorder()
			h.TokenInfo(w, req)

			require.Equal(t, http.StatusUnauthorized, w.Code)
			var resp csTokenInfoResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.False(t, resp.Valid)
		})

		t.Run("TokenInfo_missing_Authorization_header_returns_401", func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/token-info", nil)
			w := httptest.NewRecorder()
			h.TokenInfo(w, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})

		t.Run("TokenInfo_malformed_Authorization_header_returns_401", func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/token-info", nil)
			req.Header.Set("Authorization", "NotBearer sometoken")
			w := httptest.NewRecorder()
			h.TokenInfo(w, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})

		// RefreshToken must atomically rotate the bearer token:
		//   • old token → 401
		//   • new token → 200
		t.Run("RefreshToken_rotates_token_old_token_invalid_new_token_valid", func(t *testing.T) {
			// Not marked parallel — modifies and re-reads a specific session entry.
			sess := seedSession(t, "rt-rotate-001", "rt-old-token-001")

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
			req.Header.Set("Authorization", "Bearer "+sess.Token)
			w := httptest.NewRecorder()
			h.RefreshToken(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			var resp csRefreshResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.NotEmpty(t, resp.Token, "rotated token must be present")
			assert.NotEqual(t, sess.Token, resp.Token, "rotated token must differ from old token")
			assert.Greater(t, resp.ExpiresAt, time.Now().Unix())

			// Old token must be dead.
			oldReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/token-info", nil)
			oldReq.Header.Set("Authorization", "Bearer "+sess.Token)
			oldW := httptest.NewRecorder()
			h.TokenInfo(oldW, oldReq)
			assert.Equal(t, http.StatusUnauthorized, oldW.Code,
				"old token must be rejected after rotation")

			// New token must be accepted.
			newReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/token-info", nil)
			newReq.Header.Set("Authorization", "Bearer "+resp.Token)
			newW := httptest.NewRecorder()
			h.TokenInfo(newW, newReq)
			assert.Equal(t, http.StatusOK, newW.Code,
				"new token must be accepted after rotation")
		})

		t.Run("RefreshToken_missing_Authorization_header_returns_401", func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
			w := httptest.NewRecorder()
			h.RefreshToken(w, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})

		t.Run("RefreshToken_invalid_token_returns_401", func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
			req.Header.Set("Authorization", "Bearer totally-invalid-token-xyz")
			w := httptest.NewRecorder()
			h.RefreshToken(w, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	})
}
