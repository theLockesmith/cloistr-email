package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/identity"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Someone who signed up through Mail had no @cloistr.xyz address until they
// opened the Me dashboard (seen live on production, 2026-10-19): Mail never told
// cloistr-me about them, so they could not send or receive. Sign-in now asks
// cloistr-me for the address, through the real client, against a fake cloistr-me.

type fakeMe struct {
	mu     sync.Mutex
	calls  []map[string]string
	auth   []string
	status int
	delay  time.Duration
	srv    *httptest.Server
}

func newFakeMe(t *testing.T, status int, delay time.Duration) *fakeMe {
	f := &fakeMe{status: status, delay: delay}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/v1/addresses/ensure" {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.calls = append(f.calls, body)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		time.Sleep(f.delay)
		w.WriteHeader(f.status)
		_ = json.NewEncoder(w).Encode(map[string]any{"username": "brisk-otter-1234", "created": true})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMe) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func signInThrough(t *testing.T, h *Handler) (*httptest.ResponseRecorder, string) {
	t.Helper()
	store := h.sessions.(*mockStore)
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	mustSeedChallenge(t, store, "ch-ensure", "nonce-ensure")
	return doVerifyChallenge(t, h, buildSignedEvent(t, sk, "ch-ensure", "nonce-ensure")), pk
}

func TestSignIn_EnsuresAddressViaCloistrMe(t *testing.T) {
	me := newFakeMe(t, http.StatusOK, 0)
	client := identity.NewCloistrMeClient(me.srv.URL, "me-secret", zap.NewNop())
	h := &Handler{sessions: newMockStore(), logger: zap.NewNop(), addressEnsurer: client}

	w, pk := signInThrough(t, h)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, me.callCount(), "sign-in must ask cloistr-me to ensure the user's address")
	assert.Equal(t, pk, me.calls[0]["pubkey"])
	assert.Equal(t, "Bearer me-secret", me.auth[0])
}

func TestSignIn_CloistrMeFailureDoesNotBlockSignIn(t *testing.T) {
	for name, me := range map[string]*fakeMe{
		"server error": newFakeMe(t, http.StatusInternalServerError, 0),
		"slow":         newFakeMe(t, http.StatusOK, ensureAddressTimeout+2*time.Second),
	} {
		t.Run(name, func(t *testing.T) {
			client := identity.NewCloistrMeClient(me.srv.URL, "me-secret", zap.NewNop())
			h := &Handler{sessions: newMockStore(), logger: zap.NewNop(), addressEnsurer: client}

			start := time.Now()
			w, _ := signInThrough(t, h)

			require.Equal(t, http.StatusOK, w.Code, "a cloistr-me problem must not fail sign-in")
			assert.Less(t, time.Since(start), ensureAddressTimeout+time.Second, "sign-in must not wait past the ensure timeout")
		})
	}
}

func TestSignIn_NoEnsurerConfigured(t *testing.T) {
	h := &Handler{sessions: newMockStore(), logger: zap.NewNop()}
	w, _ := signInThrough(t, h)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestCloistrMeClient_EnsureAddressNoSecretIsNoop(t *testing.T) {
	me := newFakeMe(t, http.StatusOK, 0)
	client := identity.NewCloistrMeClient(me.srv.URL, "", zap.NewNop())
	require.NoError(t, client.EnsureAddress(t.Context(), "ab"))
	assert.Equal(t, 0, me.callCount())
}
