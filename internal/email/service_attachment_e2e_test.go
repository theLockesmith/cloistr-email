package email

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/blossom"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/identity"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"
)

// mockBlossomStore is a minimal in-process Blossom server for the e2e test:
// PUT /upload stores by sha256, GET /<hash> returns the blob. Auth is accepted
// without validation (the blossom package has its own auth tests).
type mockBlossomStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func (m *mockBlossomStore) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		h := hex.EncodeToString(sum[:])
		m.mu.Lock()
		m.blobs[h] = body
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		h := strings.TrimPrefix(r.URL.Path, "/")
		m.mu.Lock()
		b, ok := m.blobs[h]
		m.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(b)
	})
	return mux
}

func TestE2EAttachmentBlossomRoundTrip(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("set DATABASE_URL to run the e2e attachment test")
	}
	logger := zap.NewNop()
	ctx := context.Background()

	db, err := storage.NewPostgres(dbURL, logger)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	defer db.Close()

	sk := nostr.GeneratePrivateKey()
	pub, err := nostr.GetPublicKey(sk)
	if err != nil {
		t.Fatalf("pubkey: %v", err)
	}

	encSvc := encryption.NewEncryptionService(localSignerStore{&localNIP44Signer{sk: sk, pubkey: pub}}, logger)
	idSvc := identity.NewService(
		staticAddressStore{&identity.UnifiedAddress{Npub: pub, LocalPart: "alice", Email: "alice@cloistr.xyz", Verified: true}},
		nopResolver{}, logger,
	)

	mock := &mockBlossomStore{blobs: map[string][]byte{}}
	ts := httptest.NewServer(mock.handler())
	defer ts.Close()

	svc := NewService(idSvc, nil, encSvc, db, logger).
		WithBlossom([]blossom.Server{{URL: ts.URL, Priority: 0}}, 1)

	// Seed user + a parent email row (attachments.email_id FK).
	user := &storage.User{Npub: pub, Email: pub[:16] + "@cloistr.xyz", PublicKey: pub, EncryptionMethod: "nip44"}
	if err := db.CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	em := &storage.Email{
		UserID: user.ID, FromAddress: "alice@cloistr.xyz", ToAddress: "bob@example.com",
		Subject: "with attachment", Body: "", IsEncrypted: true,
		SenderNpub: &pub, Direction: "sent", Folder: "Sent", Status: "active",
	}
	if err := db.CreateEmail(ctx, em); err != nil {
		t.Fatalf("create email: %v", err)
	}

	// Binary attachment payload (includes non-UTF8 bytes).
	payload := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff, 0x10}

	// Upload via the real service path (server-side: encrypt-at-rest + offload).
	svc.uploadAttachments(ctx, pub, encryption.ModeServerSide, em.ID, []AttachmentInput{
		{Filename: "logo.png", ContentType: "image/png", Data: payload},
	})

	// 1. Attachment row persisted with a Blossom reference.
	atts, err := db.GetAttachmentsByEmail(ctx, em.ID)
	if err != nil {
		t.Fatalf("get attachments: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment row, got %d", len(atts))
	}
	att := atts[0]
	if att.BlossomSHA256 == nil || *att.BlossomSHA256 == "" {
		t.Fatal("attachment row missing blossom_sha256")
	}
	if att.Filename != "logo.png" {
		t.Errorf("filename = %q", att.Filename)
	}

	// 2. The blob actually landed on (mock) Blossom and is NOT the plaintext.
	mock.mu.Lock()
	blob, ok := mock.blobs[*att.BlossomSHA256]
	mock.mu.Unlock()
	if !ok {
		t.Fatal("blob not found on blossom server")
	}
	if string(blob) == string(payload) {
		t.Fatal("PLAINTEXT LEAK: attachment stored unencrypted on Blossom")
	}

	// 3. Decrypt the blob back to the original bytes (server-side self-encrypt).
	dec, err := encSvc.DecryptForRecipient(ctx, &encryption.DecryptionRequest{
		Ciphertext:      string(blob),
		RecipientPubkey: pub,
		SenderPubkey:    pub,
		Mode:            encryption.ModeServerSide,
	})
	if err != nil {
		t.Fatalf("decrypt blob: %v", err)
	}
	gotBytes, err := base64.StdEncoding.DecodeString(dec.Plaintext)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(gotBytes) != string(payload) {
		t.Fatalf("attachment round-trip mismatch:\n got:  %v\n want: %v", gotBytes, payload)
	}

	t.Logf("OK: %d-byte attachment encrypted (%d-byte ciphertext) -> Blossom -> decrypted back equal", len(payload), len(blob))
}
