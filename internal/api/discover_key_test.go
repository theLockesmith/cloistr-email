package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/identity"
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
