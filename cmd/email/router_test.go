package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/api"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/config"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

// TestEncryptionCapabilitiesRouteRegistered guards against the bug seen live
// on mail.cloistr.xyz (MAIL-7/36/37/38): the Settings page calls
// GET /api/v1/encryption/capabilities on every load
// (ui/src/lib/api.ts, encryptionAPI.getCapabilities), but the backend never
// registered that route — only /encryption/preferred-mode existed — so the
// request 404'd every time.
//
// This builds the real route table via buildRouter (the same function main()
// uses) with nil-backed handlers, which is safe because mux.Router.Match only
// matches the request against the route table; it never invokes the handler.
func TestEncryptionCapabilitiesRouteRegistered(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.Config{} // InternalAPISecret empty: internal domain-admin routes skipped.

	apiHandler := api.NewHandler(nil, nil, nil, cfg, logger)
	emailHandler := api.NewEmailHandler(nil, logger)

	router := buildRouter(nil, nil, apiHandler, emailHandler, cfg, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/encryption/capabilities", nil)
	var match mux.RouteMatch
	if !router.Match(req, &match) {
		t.Fatalf("GET /api/v1/encryption/capabilities does not match any registered route (would 404); match error: %v", match.MatchErr)
	}
	if match.Handler == nil {
		t.Fatalf("GET /api/v1/encryption/capabilities matched no handler")
	}

	// Also exercise it end-to-end through the router (not calling the handler
	// method directly) so a routing regression shows up as a real 404 here,
	// not just a mux.Match() quirk.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("GET /api/v1/encryption/capabilities returned 404 via router.ServeHTTP")
	}
}

// The UI saves a contact edit with PUT /api/v1/contacts/{id}
// (ui/src/lib/api.ts, contactAPI.update); only GET and DELETE were routed.
func TestContactRoutesRegistered(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.Config{}
	router := buildRouter(nil, nil, api.NewHandler(nil, nil, nil, cfg, logger), api.NewEmailHandler(nil, logger), cfg, logger)

	for _, mp := range [][2]string{
		{http.MethodGet, "/api/v1/contacts"},
		{http.MethodPost, "/api/v1/contacts"},
		{http.MethodGet, "/api/v1/contacts/0b6f3c1e-1111-4222-8333-944455556666"},
		{http.MethodPut, "/api/v1/contacts/0b6f3c1e-1111-4222-8333-944455556666"},
		{http.MethodDelete, "/api/v1/contacts/0b6f3c1e-1111-4222-8333-944455556666"},
	} {
		var match mux.RouteMatch
		if !router.Match(httptest.NewRequest(mp[0], mp[1], nil), &match) || match.MatchErr != nil {
			t.Errorf("%s %s is not routed (match error: %v)", mp[0], mp[1], match.MatchErr)
		}
	}
}
