package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/config"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"go.uber.org/zap"
)

// The Contacts page looked like it worked but saved nothing: ListContacts
// always answered an empty list and AddContact answered 201 with the id
// "new-contact-id" without touching the database, so every contact a user
// added vanished on reload. These drive the real handlers against a real
// Postgres built by the migrator.
//
//	TEST_PG_ADMIN_URL='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable' \
//	  go test ./internal/api -run Contacts -v

const (
	alicePK = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
	bobPK   = "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"
)

func contactsHandler(t *testing.T) http.Handler {
	t.Helper()
	adminURL := os.Getenv("TEST_PG_ADMIN_URL")
	if adminURL == "" {
		t.Skip("set TEST_PG_ADMIN_URL to run the contacts tests against a real Postgres")
	}
	admin, err := sql.Open("postgres", adminURL)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("contacts_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, _ := url.Parse(adminURL)
	u.Path = "/" + name
	sqlDB, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
	})
	// Production's role defaults to search_path email, public; a throwaway
	// superuser database does not, so pin one connection and set it there.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	db := storage.NewPostgresForTesting(sqlDB, zap.NewNop())
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := sqlDB.Exec(`SET search_path TO email, public`); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	for _, pk := range []string{alicePK, bobPK} {
		if _, err := db.EnsureMailbox(context.Background(), pk); err != nil {
			t.Fatalf("ensure mailbox: %v", err)
		}
	}

	h := NewHandler(db, nil, nil, &config.Config{}, zap.NewNop())
	r := mux.NewRouter()
	// The same routes main.go registers, minus the session middleware: the
	// caller's pubkey goes in the context the way AuthMiddleware puts it.
	c := r.PathPrefix("/api/v1/contacts").Subrouter()
	c.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := context.WithValue(req.Context(), contextKeyUserID, req.Header.Get("X-Test-User"))
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	c.HandleFunc("", h.ListContacts).Methods("GET")
	c.HandleFunc("", h.AddContact).Methods("POST")
	c.HandleFunc("/{id}", h.GetContact).Methods("GET")
	c.HandleFunc("/{id}", h.UpdateContact).Methods("PUT")
	c.HandleFunc("/{id}", h.DeleteContact).Methods("DELETE")
	return r
}

func call(t *testing.T, h http.Handler, user, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("X-Test-User", user)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func listed(t *testing.T, h http.Handler, user, query string) []map[string]any {
	t.Helper()
	code, out := call(t, h, user, http.MethodGet, "/api/v1/contacts"+query, nil)
	if code != http.StatusOK {
		t.Fatalf("list: status %d %v", code, out)
	}
	raw, _ := out["contacts"].([]any)
	var cs []map[string]any
	for _, c := range raw {
		cs = append(cs, c.(map[string]any))
	}
	if total, _ := out["total"].(float64); int(total) != len(cs) && query == "" {
		t.Fatalf("total %v but %d contacts listed", out["total"], len(cs))
	}
	return cs
}

func add(t *testing.T, h http.Handler, user, email, name string) string {
	t.Helper()
	code, out := call(t, h, user, http.MethodPost, "/api/v1/contacts", map[string]string{"email": email, "name": name})
	if code != http.StatusCreated {
		t.Fatalf("add %s: status %d %v", email, code, out)
	}
	id, _ := out["id"].(string)
	if id == "" || id == "new-contact-id" {
		t.Fatalf("add %s returned id %q, not a stored contact's id", email, id)
	}
	return id
}

func TestContactsAddedContactIsListedAndReadable(t *testing.T) {
	h := contactsHandler(t)
	id := add(t, h, alicePK, "Carol@Example.com", "Carol")

	cs := listed(t, h, alicePK, "")
	if len(cs) != 1 || cs[0]["id"] != id || cs[0]["email"] != "carol@example.com" || cs[0]["name"] != "Carol" {
		t.Fatalf("list after add = %v, want Carol (carol@example.com, id %s)", cs, id)
	}
	code, got := call(t, h, alicePK, http.MethodGet, "/api/v1/contacts/"+id, nil)
	if code != http.StatusOK || got["email"] != "carol@example.com" {
		t.Fatalf("get: status %d %v", code, got)
	}
}

func TestContactsEditAndDelete(t *testing.T) {
	h := contactsHandler(t)
	id := add(t, h, alicePK, "carol@example.com", "Carol")

	code, out := call(t, h, alicePK, http.MethodPut, "/api/v1/contacts/"+id,
		map[string]string{"name": "Carol Danvers", "organization": "Board"})
	if code != http.StatusOK {
		t.Fatalf("update: status %d %v", code, out)
	}
	cs := listed(t, h, alicePK, "")
	if len(cs) != 1 || cs[0]["name"] != "Carol Danvers" || cs[0]["organization"] != "Board" || cs[0]["email"] != "carol@example.com" {
		t.Fatalf("list after edit = %v", cs)
	}

	if code, out := call(t, h, alicePK, http.MethodDelete, "/api/v1/contacts/"+id, nil); code != http.StatusOK {
		t.Fatalf("delete: status %d %v", code, out)
	}
	if cs := listed(t, h, alicePK, ""); len(cs) != 0 {
		t.Fatalf("list after delete = %v, want empty", cs)
	}
	if code, _ := call(t, h, alicePK, http.MethodGet, "/api/v1/contacts/"+id, nil); code != http.StatusNotFound {
		t.Fatalf("get after delete: status %d, want 404", code)
	}
	// Adding the same address again works and brings it back.
	add(t, h, alicePK, "carol@example.com", "Carol again")
	if cs := listed(t, h, alicePK, ""); len(cs) != 1 || cs[0]["name"] != "Carol again" {
		t.Fatalf("list after re-add = %v", cs)
	}
}

func TestContactsAreOnlyTheOwnersBusiness(t *testing.T) {
	h := contactsHandler(t)
	id := add(t, h, alicePK, "carol@example.com", "Carol")

	if cs := listed(t, h, bobPK, ""); len(cs) != 0 {
		t.Fatalf("bob sees alice's contacts: %v", cs)
	}
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		code, _ := call(t, h, bobPK, m, "/api/v1/contacts/"+id, map[string]string{"name": "pwned"})
		if code != http.StatusNotFound {
			t.Errorf("bob %s alice's contact: status %d, want 404", m, code)
		}
	}
	if cs := listed(t, h, alicePK, ""); len(cs) != 1 || cs[0]["name"] != "Carol" {
		t.Fatalf("alice's contact changed by bob: %v", cs)
	}
}

func TestContactsDuplicateBadIDAndSearch(t *testing.T) {
	h := contactsHandler(t)
	add(t, h, alicePK, "carol@example.com", "Carol")
	add(t, h, alicePK, "dave@example.com", "Dave")

	if code, out := call(t, h, alicePK, http.MethodPost, "/api/v1/contacts",
		map[string]string{"email": "CAROL@example.com", "name": "Carol 2"}); code != http.StatusConflict {
		t.Fatalf("duplicate add: status %d %v, want 409", code, out)
	}
	if code, out := call(t, h, alicePK, http.MethodPost, "/api/v1/contacts",
		map[string]string{"email": "not an address"}); code != http.StatusBadRequest {
		t.Fatalf("invalid email: status %d %v, want 400", code, out)
	}
	if code, _ := call(t, h, alicePK, http.MethodGet, "/api/v1/contacts/not-a-uuid", nil); code != http.StatusNotFound {
		t.Fatalf("bad id: status %d, want 404", code)
	}
	cs := listed(t, h, alicePK, "?search=dav")
	if len(cs) != 1 || cs[0]["email"] != "dave@example.com" {
		t.Fatalf("search dav = %v", cs)
	}
	if !strings.Contains(fmt.Sprint(listed(t, h, alicePK, "")), "carol@example.com") {
		t.Fatal("full list lost carol")
	}
}
