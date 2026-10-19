package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/identity"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/ratelimit"
	"go.uber.org/zap"
)

type fakeResolver map[string]*identity.ExternalRecipient

func (f fakeResolver) ResolveRecipient(_ context.Context, email string) (*identity.ExternalRecipient, error) {
	if email == "broken@example.com" {
		return nil, errors.New("store down")
	}
	if r, ok := f[email]; ok {
		return r, nil
	}
	return &identity.ExternalRecipient{Email: email, DiscoveryMethod: "none"}, nil
}

func discover(t *testing.T, h *Handler, email string) DiscoverKeyResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/keys/discover?email="+email, nil)
	req = req.WithContext(context.WithValue(req.Context(), contextKeyUserID, "npub-test"))
	rec := httptest.NewRecorder()
	h.DiscoverKey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out DiscoverKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// DiscoverKey was a stub that always answered found:false, so the compose
// page could never offer end-to-end encryption to anyone. It now resolves
// through the same identity service the send path uses (internal addresses,
// then NIP-05), so a key found here is the key a client-mode send must use.
func TestDiscoverKey_FindsKeyedRecipient(t *testing.T) {
	bob := strings.Repeat("b", 64)
	carol := strings.Repeat("c", 64)
	h := &Handler{logger: zap.NewNop()}
	h.SetRecipientResolver(fakeResolver{
		"bob@cloistr.xyz":   {Email: "bob@cloistr.xyz", Npub: bob, DiscoveryMethod: "internal"},
		"carol@example.com": {Email: "carol@example.com", Npub: carol, DiscoveryMethod: "nip05"},
	})

	got := discover(t, h, "bob@cloistr.xyz")
	if !got.Found || got.Pubkey != bob || got.Source != "internal" || !strings.HasPrefix(got.Npub, "npub1") {
		t.Errorf("internal recipient: %+v", got)
	}
	got = discover(t, h, "carol@example.com")
	if !got.Found || got.Pubkey != carol || got.Source != "nip05" {
		t.Errorf("NIP-05 recipient: %+v", got)
	}
	if got := discover(t, h, "nobody@example.com"); got.Found || got.Pubkey != "" {
		t.Errorf("unkeyed recipient reported found: %+v", got)
	}
	if got := discover(t, h, "broken@example.com"); got.Found {
		t.Errorf("lookup error reported found: %+v", got)
	}
}

type countingResolver struct {
	fakeResolver
	calls int
}

func (c *countingResolver) ResolveRecipient(ctx context.Context, email string) (*identity.ExternalRecipient, error) {
	c.calls++
	return c.fakeResolver.ResolveRecipient(ctx, email)
}

func discoverStatus(h *Handler, email string) int {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/keys/discover?email="+url.QueryEscape(email), nil)
	req = req.WithContext(context.WithValue(req.Context(), contextKeyUserID, "npub-test"))
	rec := httptest.NewRecorder()
	h.DiscoverKey(rec, req)
	return rec.Code
}

// Each discovery is an outbound NIP-05 fetch on the caller's behalf, so it is
// rate-limited per account, and a malformed address is refused before any
// lookup happens.
func TestDiscoverKey_ValidatesAndRateLimits(t *testing.T) {
	res := &countingResolver{fakeResolver: fakeResolver{}}
	h := &Handler{logger: zap.NewNop()}
	h.SetRecipientResolver(res)
	h.SetDiscoverLimiter(ratelimit.NewDiscoverLimiter(ratelimit.NewMemoryStore(), 2, 0, 0))

	for _, bad := range []string{"not-an-address", "a@", "@b.com", "a b@c.com"} {
		if code := discoverStatus(h, bad); code != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", bad, code)
		}
	}
	if res.calls != 0 {
		t.Fatalf("resolver called %d times for malformed addresses", res.calls)
	}
	for i := 0; i < 2; i++ {
		if code := discoverStatus(h, "x@example.com"); code != http.StatusOK {
			t.Fatalf("lookup %d: status %d", i+1, code)
		}
	}
	if code := discoverStatus(h, "x@example.com"); code != http.StatusTooManyRequests {
		t.Errorf("third lookup in a minute: status %d, want 429", code)
	}
	if res.calls != 2 {
		t.Errorf("resolver called %d times, want 2 (the limited request must not resolve)", res.calls)
	}
}
