package blossom

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// testPrivKey is a throwaway key for signing auth events in tests.
const testPrivKey = "67dea2ed018072d675f5415ecfaed7d2597555e202d85b3d65ea4e58d2d92ffa"

func newTestSigner(t *testing.T) *LocalAuthSigner {
	t.Helper()
	s, err := NewLocalAuthSigner(testPrivKey)
	if err != nil {
		t.Fatalf("NewLocalAuthSigner: %v", err)
	}
	return s
}

// mockBlossom is an in-process Blossom server (BUD-01/02 subset) backed by a
// map. It validates the Authorization header on upload/delete.
type mockBlossom struct {
	mu       sync.Mutex
	blobs    map[string][]byte
	failNext bool // if set, the next upload returns 500
}

func newMockBlossom() *mockBlossom { return &mockBlossom{blobs: map[string][]byte{}} }

func (m *mockBlossom) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !validAuth(r, "upload") {
			w.Header().Set("X-Reason", "bad auth")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		m.mu.Lock()
		fail := m.failNext
		m.failNext = false
		m.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		hash := hex.EncodeToString(sum[:])
		m.mu.Lock()
		m.blobs[hash] = body
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"sha256": hash, "size": len(body)})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hash := strings.TrimPrefix(r.URL.Path, "/")
		switch r.Method {
		case http.MethodGet:
			m.mu.Lock()
			body, ok := m.blobs[hash]
			m.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(body)
		case http.MethodDelete:
			if !validAuth(r, "delete") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			m.mu.Lock()
			delete(m.blobs, hash)
			m.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	return mux
}

// validAuth checks the "Authorization: Nostr <base64-event>" header for a
// valid signature, matching verb, and unexpired event.
func validAuth(r *http.Request, verb string) bool {
	h := r.Header.Get("Authorization")
	raw, ok := strings.CutPrefix(h, "Nostr ")
	if !ok {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return false
	}
	var ev nostr.Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return false
	}
	if ev.Kind != authEventKind {
		return false
	}
	if ok, _ := ev.CheckSignature(); !ok {
		return false
	}
	var gotVerb string
	var exp int64
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "t":
			gotVerb = tag[1]
		case "expiration":
			exp, _ = strconv.ParseInt(tag[1], 10, 64)
		}
	}
	if gotVerb != verb {
		return false
	}
	if exp != 0 && time.Now().Unix() > exp {
		return false
	}
	return true
}

func serverFor(ts *httptest.Server, priority int) Server {
	return Server{URL: ts.URL, Priority: priority}
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	mock := newMockBlossom()
	ts := httptest.NewServer(mock.handler())
	defer ts.Close()

	c := NewClient(newTestSigner(t), nil)
	payload := []byte("nip44-ciphertext-goes-here")

	desc, err := c.Upload(context.Background(), payload, "application/octet-stream", []Server{serverFor(ts, 1)}, 1)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if desc.SHA256 != sha256Hex(payload) {
		t.Errorf("descriptor hash mismatch: got %s", desc.SHA256)
	}
	if len(desc.Servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(desc.Servers))
	}

	got, err := c.Download(context.Background(), desc.SHA256, []Server{serverFor(ts, 1)})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("round-trip mismatch: got %q", got)
	}
}

func TestUploadRedundancyAndFallback(t *testing.T) {
	mockA := newMockBlossom()
	mockA.failNext = true // first server rejects the upload
	tsA := httptest.NewServer(mockA.handler())
	defer tsA.Close()
	mockB := newMockBlossom()
	tsB := httptest.NewServer(mockB.handler())
	defer tsB.Close()

	c := NewClient(newTestSigner(t), nil)
	payload := []byte("redundant-blob")
	servers := []Server{serverFor(tsA, 1), serverFor(tsB, 2)}

	// redundancy 2 but server A fails once → should still succeed on B.
	desc, err := c.Upload(context.Background(), payload, "", servers, 2)
	if err != nil {
		t.Fatalf("Upload with fallback: %v", err)
	}
	if len(desc.Servers) != 1 || desc.Servers[0] != normalizeURL(tsB.URL) {
		t.Errorf("expected blob only on server B, got %v", desc.Servers)
	}

	// Download should fall back past A (which never stored it) to B.
	got, err := c.Download(context.Background(), desc.SHA256, servers)
	if err != nil {
		t.Fatalf("Download fallback: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("fallback mismatch: got %q", got)
	}
}

func TestDownloadHashMismatchSkipsServer(t *testing.T) {
	// Server returns data that does not match the requested hash.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("tampered"))
	}))
	defer bad.Close()
	good := newMockBlossom()
	tsGood := httptest.NewServer(good.handler())
	defer tsGood.Close()

	c := NewClient(newTestSigner(t), nil)
	payload := []byte("authentic-content")
	hash := sha256Hex(payload)
	good.blobs[hash] = payload

	got, err := c.Download(context.Background(), hash, []Server{serverFor(bad, 1), serverFor(tsGood, 2)})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("expected to skip tampered server, got %q", got)
	}
}

func TestDeleteRemovesBlob(t *testing.T) {
	mock := newMockBlossom()
	ts := httptest.NewServer(mock.handler())
	defer ts.Close()

	c := NewClient(newTestSigner(t), nil)
	payload := []byte("delete-me")
	desc, err := c.Upload(context.Background(), payload, "", []Server{serverFor(ts, 1)}, 1)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if err := c.Delete(context.Background(), desc.SHA256, []Server{serverFor(ts, 1)}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := c.Download(context.Background(), desc.SHA256, []Server{serverFor(ts, 1)}); err == nil {
		t.Errorf("expected download to fail after delete")
	}
}

func TestDownloadRejectsInvalidHash(t *testing.T) {
	c := NewClient(newTestSigner(t), nil)
	srv := []Server{{URL: "http://example.invalid", Priority: 1}}
	for _, bad := range []string{"", "abc", "../etc/passwd", strings.Repeat("Z", 64)} {
		if _, err := c.Download(context.Background(), bad, srv); err == nil {
			t.Errorf("expected invalid hash %q to be rejected", bad)
		}
		if err := c.Delete(context.Background(), bad, srv); err == nil {
			t.Errorf("expected Delete to reject invalid hash %q", bad)
		}
	}
}

func TestDownloadRejectsOversizeBlob(t *testing.T) {
	// Server streams more than maxBlobSize bytes.
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		buf := make([]byte, 1<<20)
		for written := 0; written <= maxBlobSize; written += len(buf) {
			w.Write(buf)
		}
	}))
	defer big.Close()

	c := NewClient(newTestSigner(t), nil)
	hash := strings.Repeat("a", 64) // valid-shaped hash
	_, err := c.Download(context.Background(), hash, []Server{serverFor(big, 1)})
	if err == nil {
		t.Fatal("expected oversize blob to be rejected")
	}
}

func TestSignerRedactsPrivateKey(t *testing.T) {
	s := newTestSigner(t)
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(format, s)
		if strings.Contains(out, testPrivKey) {
			t.Errorf("format %q leaked private key: %s", format, out)
		}
	}
}

func TestUploadNoServers(t *testing.T) {
	c := NewClient(newTestSigner(t), nil)
	if _, err := c.Upload(context.Background(), []byte("x"), "", nil, 1); err != ErrNoServers {
		t.Errorf("expected ErrNoServers, got %v", err)
	}
}

func TestUploadRejectedWithoutValidAuth(t *testing.T) {
	// A server that requires a "delete" verb will reject our upload auth,
	// proving the client's signed verb actually reaches the server and is
	// enforced.
	rejectUploads := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validAuth(r, "delete") { // upload auth carries verb "upload", so this fails
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer rejectUploads.Close()

	c := NewClient(newTestSigner(t), nil)
	if _, err := c.Upload(context.Background(), []byte("y"), "", []Server{serverFor(rejectUploads, 1)}, 1); err == nil {
		t.Errorf("expected upload to be rejected when server demands a different verb")
	}
}
