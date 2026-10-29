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

// The v1 email list/read/reply/delete and key-import handlers were TODO stubs
// that answered success without doing anything ("replied", "deleted",
// "imported", an always-empty list). Nothing in the UI calls them; a headless
// client that did was told its action worked. They must not be routed.
func TestV1StubRoutesNotRegistered(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.Config{}
	router := buildRouter(nil, nil, api.NewHandler(nil, nil, nil, cfg, logger), api.NewEmailHandler(nil, logger), cfg, logger)

	for _, mp := range [][2]string{
		{http.MethodGet, "/api/v1/emails"},
		{http.MethodGet, "/api/v1/emails/0b6f3c1e-1111-4222-8333-944455556666"},
		{http.MethodPost, "/api/v1/emails/0b6f3c1e-1111-4222-8333-944455556666/reply"},
		{http.MethodDelete, "/api/v1/emails/0b6f3c1e-1111-4222-8333-944455556666"},
		{http.MethodPost, "/api/v1/keys/import"},
	} {
		var match mux.RouteMatch
		if router.Match(httptest.NewRequest(mp[0], mp[1], nil), &match) && match.MatchErr == nil {
			t.Errorf("%s %s is still routed to a stub that fakes success", mp[0], mp[1])
		}
	}
	// The real routes beside them stay.
	for _, mp := range [][2]string{
		{http.MethodGet, "/api/v1/keys/discover"},
		{http.MethodGet, "/api/v2/email"},
	} {
		var match mux.RouteMatch
		if !router.Match(httptest.NewRequest(mp[0], mp[1], nil), &match) || match.MatchErr != nil {
			t.Errorf("%s %s should still be routed", mp[0], mp[1])
		}
	}
}

// TestEmailStatsRouteRegistered: the Inbox unread badge calls
// GET /api/v2/email/stats. Without its own route, gorilla/mux hands "stats"
// to GET /api/v2/email/{id} and the badge gets "email not found".
func TestEmailStatsRouteRegistered(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.Config{}

	apiHandler := api.NewHandler(nil, nil, nil, cfg, logger)
	emailHandler := api.NewEmailHandler(nil, logger)

	router := buildRouter(nil, nil, apiHandler, emailHandler, cfg, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/email/stats", nil)
	var match mux.RouteMatch
	if !router.Match(req, &match) {
		t.Fatalf("GET /api/v2/email/stats does not match any route; match error: %v", match.MatchErr)
	}
	tpl, err := match.Route.GetPathTemplate()
	if err != nil {
		t.Fatalf("path template: %v", err)
	}
	if tpl != "/api/v2/email/stats" {
		t.Fatalf("GET /api/v2/email/stats routed to %q, want its own route", tpl)
	}
}

// TestDraftRoutesRegistered: Compose saves drafts with POST /api/v2/email/drafts
// and PUT /api/v2/email/drafts/{id}. The PUT must not fall through to any
// /{id} route.
func TestDraftRoutesRegistered(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.Config{}

	apiHandler := api.NewHandler(nil, nil, nil, cfg, logger)
	emailHandler := api.NewEmailHandler(nil, logger)

	router := buildRouter(nil, nil, apiHandler, emailHandler, cfg, logger)

	for _, tc := range []struct{ method, path, want string }{
		{http.MethodPost, "/api/v2/email/drafts", "/api/v2/email/drafts"},
		{http.MethodPut, "/api/v2/email/drafts/abc", "/api/v2/email/drafts/{id}"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		var match mux.RouteMatch
		if !router.Match(req, &match) {
			t.Fatalf("%s %s does not match any route; match error: %v", tc.method, tc.path, match.MatchErr)
		}
		tpl, err := match.Route.GetPathTemplate()
		if err != nil {
			t.Fatalf("path template: %v", err)
		}
		if tpl != tc.want {
			t.Fatalf("%s %s routed to %q, want %q", tc.method, tc.path, tpl, tc.want)
		}
	}
}
