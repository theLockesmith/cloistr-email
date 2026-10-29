package api

import (
	"context"
	"net/http"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/config"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

// Settings offers "client" (NIP-07, zero-knowledge) as the default mode and
// posts it as "client", the same name the rest of the service uses for that
// mode. The handler only accepted "e2e", so saving it answered 400 and a
// client-side preference could never be stored.
//
//	TEST_PG_ADMIN_URL='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable' \
//	  go test ./internal/api -run EncryptionPreference -v

func preferenceHandler(t *testing.T) http.Handler {
	t.Helper()
	db := testDB(t)
	h := NewHandler(db, nil, nil, &config.Config{}, zap.NewNop())
	r := mux.NewRouter()
	e := r.PathPrefix("/api/v1/encryption").Subrouter()
	e.Use(testUserMiddleware)
	e.HandleFunc("/preferred-mode", h.GetEncryptionPreference).Methods("GET")
	e.HandleFunc("/preferred-mode", h.SetEncryptionPreference).Methods("POST")
	return r
}

func TestEncryptionPreferenceSavesClientSide(t *testing.T) {
	h := preferenceHandler(t)
	code, out := call(t, h, alicePK, http.MethodPost, "/api/v1/encryption/preferred-mode", map[string]string{"mode": "client"})
	if code != http.StatusOK {
		t.Fatalf("save client: status %d %v, want 200", code, out)
	}
	code, out = call(t, h, alicePK, http.MethodGet, "/api/v1/encryption/preferred-mode", nil)
	if code != http.StatusOK || out["mode"] != "client" {
		t.Fatalf("read back: status %d mode %v, want 200 client", code, out["mode"])
	}
}

func TestEncryptionPreferenceLegacyE2EReadsAsClient(t *testing.T) {
	h := preferenceHandler(t)
	code, out := call(t, h, alicePK, http.MethodPost, "/api/v1/encryption/preferred-mode", map[string]string{"mode": "e2e"})
	if code != http.StatusOK || out["mode"] != "client" {
		t.Fatalf("save e2e: status %d mode %v, want 200 client", code, out["mode"])
	}
	code, out = call(t, h, alicePK, http.MethodGet, "/api/v1/encryption/preferred-mode", nil)
	if code != http.StatusOK || out["mode"] != "client" {
		t.Fatalf("read back: status %d mode %v, want 200 client", code, out["mode"])
	}
}

func TestEncryptionPreferenceStoredE2EReadsAsClient(t *testing.T) {
	db := testDB(t)
	if err := db.SetPreferredEncryptionMode(context.Background(), bobPK, "e2e"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := NewHandler(db, nil, nil, &config.Config{}, zap.NewNop())
	r := mux.NewRouter()
	r.Use(testUserMiddleware)
	r.HandleFunc("/api/v1/encryption/preferred-mode", h.GetEncryptionPreference).Methods("GET")
	code, out := call(t, r, bobPK, http.MethodGet, "/api/v1/encryption/preferred-mode", nil)
	if code != http.StatusOK || out["mode"] != "client" {
		t.Fatalf("read stored e2e: status %d mode %v, want 200 client", code, out["mode"])
	}
}

func TestEncryptionPreferenceRejectsUnknownMode(t *testing.T) {
	h := preferenceHandler(t)
	for _, mode := range []string{"bogus", "", "CLIENT"} {
		code, _ := call(t, h, alicePK, http.MethodPost, "/api/v1/encryption/preferred-mode", map[string]string{"mode": mode})
		if code != http.StatusBadRequest {
			t.Errorf("mode %q: status %d, want 400", mode, code)
		}
	}
}
