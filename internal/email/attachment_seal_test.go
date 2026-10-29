package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/blobseal"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/blossom"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/identity"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"
)

// recordingBlossom is a fake Blossom server that keeps what each upload
// carried: the bytes, the declared Content-Type and the signing pubkey.
type recordingBlossom struct {
	mu      sync.Mutex
	blobs   map[string][]byte
	types   map[string]string
	signers map[string]string
}

func newRecordingBlossom() *recordingBlossom {
	return &recordingBlossom{blobs: map[string][]byte{}, types: map[string]string{}, signers: map[string]string{}}
}

func (m *recordingBlossom) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(body)
		h := hex.EncodeToString(sum[:])
		signer := ""
		if raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(r.Header.Get("Authorization"), "Nostr ")); err == nil {
			var ev nostr.Event
			if json.Unmarshal(raw, &ev) == nil {
				if ok, _ := ev.CheckSignature(); ok {
					signer = ev.PubKey
				}
			}
		}
		m.mu.Lock()
		m.blobs[h], m.types[h], m.signers[h] = body, r.Header.Get("Content-Type"), signer
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		b, ok := m.blobs[strings.TrimPrefix(r.URL.Path, "/")]
		m.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	})
	return mux
}

func (m *recordingBlossom) get(h string) (blob []byte, contentType, signer string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blobs[h], m.types[h], m.signers[h]
}

func newTestSealer(t *testing.T) *blobseal.Sealer {
	t.Helper()
	k := make([]byte, blobseal.KEKSize)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	s, err := blobseal.NewSealer(base64.StdEncoding.EncodeToString(k))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newServiceSigner(t *testing.T) *blossom.LocalAuthSigner {
	t.Helper()
	s, err := blossom.NewLocalAuthSigner(nostr.GeneratePrivateKey())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var secretPDF = []byte("%PDF-1.4 payroll for October: CONFIDENTIAL\x00\xff\x10")

// Inbound attachments are sealed before upload, uploaded as opaque
// octet-stream, and signed by Mail's service key (the recipient is offline and
// the server holds no session for them).
func TestInboundUploaderSealsAndSignsAsService(t *testing.T) {
	fake := newRecordingBlossom()
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()

	sealer := newTestSealer(t)
	svcKey := newServiceSigner(t)
	up := NewBlossomAttachmentUploader(svcKey, sealer, []blossom.Server{{URL: ts.URL}}, 1, zap.NewNop())
	if up == nil {
		t.Fatal("uploader not built")
	}

	got, err := up.UploadFor(context.Background(), secretPDF)
	if err != nil {
		t.Fatalf("UploadFor: %v", err)
	}
	blob, ctype, signer := fake.get(got.SHA256)
	if blob == nil {
		t.Fatal("nothing uploaded under the returned hash")
	}
	if bytes.Contains(blob, secretPDF) || bytes.Contains(blob, []byte("CONFIDENTIAL")) {
		t.Fatal("PLAINTEXT LEAK: uploaded bytes contain the attachment")
	}
	if ctype != "application/octet-stream" {
		t.Errorf("uploaded Content-Type = %q, want application/octet-stream", ctype)
	}
	if signer != svcKey.PublicKey() {
		t.Errorf("upload signed by %q, want the Mail service key %q", signer, svcKey.PublicKey())
	}
	if got.Key == "" {
		t.Fatal("no wrapped key returned")
	}
	opened, err := sealer.Open(got.Key, blob)
	if err != nil || !bytes.Equal(opened, secretPDF) {
		t.Fatalf("returned key does not open the uploaded blob: %v", err)
	}
}

// Without a sealer (or a service key) there is no uploader at all, so no
// configuration can put plaintext on Blossom.
func TestInboundUploaderRequiresSealerAndKey(t *testing.T) {
	servers := []blossom.Server{{URL: "http://127.0.0.1:1"}}
	if NewBlossomAttachmentUploader(newServiceSigner(t), nil, servers, 1, zap.NewNop()) != nil {
		t.Error("uploader built without a sealer")
	}
	if NewBlossomAttachmentUploader(nil, newTestSealer(t), servers, 1, zap.NewNop()) != nil {
		t.Error("uploader built without a service key")
	}
	if NewBlossomAttachmentUploader(newServiceSigner(t), newTestSealer(t), nil, 1, zap.NewNop()) != nil {
		t.Error("uploader built without servers")
	}
}

// TestE2ESealedAttachments covers storage and download with a real database:
// inbound and outbound (unencrypted mode) attachments are stored sealed with
// their wrapped key, GetAttachment returns the original bytes, and a missing,
// tampered or wrong key fails closed with no bytes.
//
//	Run with: DATABASE_URL='postgres://...?sslmode=disable&search_path=email,public' \
//	  go test ./internal/email/ -run SealedAttachments -v
func TestE2ESealedAttachments(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("set DATABASE_URL to run the e2e sealed-attachment test")
	}
	logger := zap.NewNop()
	ctx := context.Background()

	db, err := storage.NewPostgres(dbURL, logger)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	fake := newRecordingBlossom()
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()
	servers := []blossom.Server{{URL: ts.URL}}

	sk := nostr.GeneratePrivateKey()
	me, _ := nostr.GetPublicKey(sk)
	if _, err := db.EnsureMailbox(ctx, me); err != nil {
		t.Fatalf("ensure mailbox: %v", err)
	}
	encSvc := encryption.NewEncryptionService(localSignerStore{&localNIP44Signer{sk: sk, pubkey: me}}, logger)
	idSvc := identity.NewService(
		staticAddressStore{&identity.UnifiedAddress{Npub: me, LocalPart: "alice", Email: "alice@cloistr.xyz", Verified: true}},
		nopResolver{}, logger,
	)
	sealer := newTestSealer(t)
	svcKey := newServiceSigner(t)
	svc := NewService(idSvc, nil, encSvc, db, logger).
		WithBlossom(servers, 1).
		WithAttachmentSealing(sealer, svcKey)

	mkEmail := func(direction string) *storage.Email {
		none := "none"
		e := &storage.Email{
			MailboxPubkey: me, FromAddress: "x@example.org", ToAddress: "alice@cloistr.xyz",
			Subject: "with file", Body: "b", EncryptionMode: &none, Direction: direction,
		}
		if err := db.CreateEmail(ctx, e); err != nil {
			t.Fatalf("create email: %v", err)
		}
		return e
	}
	onlyAttachment := func(emailID string) *storage.Attachment {
		t.Helper()
		atts, err := db.GetAttachmentsByEmail(ctx, emailID)
		if err != nil || len(atts) != 1 {
			t.Fatalf("attachments for %s: %d, %v", emailID, len(atts), err)
		}
		return atts[0]
	}
	assertSealedOnBlossom := func(att *storage.Attachment) {
		t.Helper()
		if att.BlossomSHA256 == nil || att.BlobKey == nil || *att.BlobKey == "" {
			t.Fatalf("attachment row missing blob hash or key: %+v", att)
		}
		blob, _, signer := fake.get(*att.BlossomSHA256)
		if blob == nil {
			t.Fatal("blob not on Blossom")
		}
		if bytes.Contains(blob, secretPDF) {
			t.Fatal("PLAINTEXT LEAK: stored blob contains the attachment")
		}
		if signer != svcKey.PublicKey() {
			t.Errorf("upload signed by %q, want service key", signer)
		}
	}

	// Inbound.
	inbound := mkEmail("received")
	proc := NewInboundProcessor(db, nil, logger)
	proc.SetAttachmentUploader(NewBlossomAttachmentUploader(svcKey, sealer, servers, 1, logger))
	proc.storeAttachments(ctx, inbound.ID, me, []ParsedAttachment{{Filename: "payroll.pdf", ContentType: "application/pdf", Data: secretPDF}})
	inAtt := onlyAttachment(inbound.ID)
	assertSealedOnBlossom(inAtt)
	res, err := svc.GetAttachment(ctx, me, inbound.ID, inAtt.ID)
	if err != nil {
		t.Fatalf("GetAttachment inbound: %v", err)
	}
	if !bytes.Equal(res.Data, secretPDF) || res.ContentType != "application/pdf" {
		t.Fatalf("inbound round trip = %q (%s)", res.Data, res.ContentType)
	}

	// Outbound, unencrypted mode: sealed the same way, never plaintext.
	sent := mkEmail("sent")
	svc.uploadAttachments(ctx, me, encryption.ModeNone, sent.ID, []AttachmentInput{{Filename: "payroll.pdf", ContentType: "application/pdf", Data: secretPDF}})
	outAtt := onlyAttachment(sent.ID)
	assertSealedOnBlossom(outAtt)
	res, err = svc.GetAttachment(ctx, me, sent.ID, outAtt.ID)
	if err != nil || !bytes.Equal(res.Data, secretPDF) {
		t.Fatalf("outbound round trip: %v", err)
	}

	// Fail closed.
	setKey := func(id string, key *string) {
		t.Helper()
		if _, err := db.DB().ExecContext(ctx, `UPDATE attachments SET blob_key = $2 WHERE id = $1`, id, key); err != nil {
			t.Fatalf("set key: %v", err)
		}
	}
	_, otherKey, _ := newTestSealer(t).Seal([]byte("another file"))
	tampered := *inAtt.BlobKey
	tampered = tampered[:len(tampered)-2] + "AA"
	for name, key := range map[string]*string{"missing key": nil, "key from another KEK": &otherKey, "tampered key": &tampered} {
		setKey(inAtt.ID, key)
		res, err := svc.GetAttachment(ctx, me, inbound.ID, inAtt.ID)
		if err == nil {
			t.Errorf("%s: GetAttachment succeeded", name)
		}
		if res != nil && len(res.Data) > 0 {
			t.Errorf("%s: returned %d bytes", name, len(res.Data))
		}
	}
	setKey(inAtt.ID, inAtt.BlobKey)

	// A service with a different KEK cannot open it either.
	wrongKEK := NewService(idSvc, nil, encSvc, db, logger).WithBlossom(servers, 1).WithAttachmentSealing(newTestSealer(t), svcKey)
	if res, err := wrongKEK.GetAttachment(ctx, me, inbound.ID, inAtt.ID); err == nil || (res != nil && len(res.Data) > 0) {
		t.Error("wrong KEK opened the attachment")
	}
	// Without sealing configured, a sealed row is refused rather than served raw.
	noSeal := NewService(idSvc, nil, encSvc, db, logger).WithBlossom(servers, 1)
	if res, err := noSeal.GetAttachment(ctx, me, inbound.ID, inAtt.ID); err == nil || (res != nil && len(res.Data) > 0) {
		t.Error("service without a sealer served a sealed blob")
	}
	// And without sealing, unencrypted-mode uploads are skipped, not sent plaintext.
	plainSent := mkEmail("sent")
	noSeal.uploadAttachments(ctx, me, encryption.ModeNone, plainSent.ID, []AttachmentInput{{Filename: "p.pdf", ContentType: "application/pdf", Data: secretPDF}})
	if atts, _ := db.GetAttachmentsByEmail(ctx, plainSent.ID); len(atts) != 0 {
		t.Errorf("unsealed service stored %d attachment(s) for an unencrypted send", len(atts))
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for h, b := range fake.blobs {
		if bytes.Contains(b, secretPDF) {
			t.Fatalf("PLAINTEXT LEAK: blob %s on Blossom is the attachment", h)
		}
	}
}
