package email

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr/nip44"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
)

// Unencrypted mode never puts a readable file on Blossom: with a sealer the
// blob is sealed and its wrapped key returned; without one it is refused.
func TestAttachmentBlob_NoneIsSealed(t *testing.T) {
	data := []byte{0x00, 0x01, 0x02, 0xff, 'h', 'i'}

	s := newServiceWithSigner(nil)
	if blob, key, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeNone, AttachmentInput{Data: data}); !errors.Is(err, ErrAttachmentNotSealable) || blob != nil || key != "" {
		t.Fatalf("without a sealer: blob=%v key=%q err=%v, want ErrAttachmentNotSealable", blob, key, err)
	}

	sealer := newTestSealer(t)
	s.WithAttachmentSealing(sealer, newServiceSigner(t))
	blob, key, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeNone, AttachmentInput{Data: data})
	if err != nil {
		t.Fatalf("attachmentBlob: %v", err)
	}
	if string(blob) == string(data) || key == "" {
		t.Fatal("unencrypted-mode attachment was not sealed")
	}
	if got, err := sealer.Open(key, blob); err != nil || string(got) != string(data) {
		t.Fatalf("sealed blob does not open: %v", err)
	}
}

// clientEnvelope returns a client-encrypted attachment exactly as the
// browser's attachment-crypto.ts produces it (fixture shared with the
// encryption package's tests).
func clientEnvelope(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../encryption/testdata/browser_encrypted_attachment.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func bareNIP44(t *testing.T) []byte {
	t.Helper()
	var key [32]byte
	ct, err := nip44.Encrypt(base64.StdEncoding.EncodeToString([]byte("file bytes")), key)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(ct)
}

func TestAttachmentBlob_ClientPassThrough(t *testing.T) {
	s := newServiceWithSigner(nil)
	ciphertext := clientEnvelope(t)
	blob, _, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeClientSide, AttachmentInput{Data: ciphertext})
	if err != nil {
		t.Fatalf("attachmentBlob: %v", err)
	}
	if string(blob) != string(ciphertext) {
		t.Errorf("client mode should pass ciphertext through unchanged")
	}
}

// Client mode promises the server never sees plaintext. A raw file in a
// client-mode send used to be uploaded to Blossom as-is (and Blossom GET is
// unauthenticated); it must be refused instead.
func TestAttachmentBlob_ClientRefusesPlaintext(t *testing.T) {
	s := newServiceWithSigner(nil)
	for name, data := range map[string][]byte{
		"raw bytes":        {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
		"base64 plaintext": []byte(base64.StdEncoding.EncodeToString([]byte("a plaintext file, base64'd by the browser, long enough to pass a length check on its own............"))),
		// The earlier stopgap sent the file through NIP-44 directly; that format
		// is replaced by the envelope and is no longer accepted.
		"bare NIP-44 payload": bareNIP44(t),
	} {
		t.Run(name, func(t *testing.T) {
			blob, _, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeClientSide, AttachmentInput{Filename: "f", Data: data})
			if !errors.Is(err, ErrClientAttachmentNotEncrypted) {
				t.Fatalf("err = %v, want ErrClientAttachmentNotEncrypted", err)
			}
			if blob != nil {
				t.Error("no bytes may be returned for upload")
			}
		})
	}
}

func TestCheckClientAttachmentsEncrypted(t *testing.T) {
	good := AttachmentInput{Filename: "ok.pdf", Data: clientEnvelope(t)}
	bad := AttachmentInput{Filename: "leak.pdf", Data: []byte("%PDF-1.7 plaintext")}

	if err := checkClientAttachmentsEncrypted(nil); err != nil {
		t.Errorf("no attachments: %v", err)
	}
	if err := checkClientAttachmentsEncrypted([]AttachmentInput{good}); err != nil {
		t.Errorf("all ciphertext: %v", err)
	}
	err := checkClientAttachmentsEncrypted([]AttachmentInput{good, bad})
	if !errors.Is(err, ErrClientAttachmentNotEncrypted) {
		t.Fatalf("one plaintext attachment: err = %v, want ErrClientAttachmentNotEncrypted", err)
	}
}

func TestAttachmentBlob_ServerEncryptsBase64(t *testing.T) {
	s := newServiceWithSigner(&fakeSigner{pubkey: "senderpub", canEnc: true})
	data := []byte{0xde, 0xad, 0xbe, 0xef}
	blob, _, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeServerSide, AttachmentInput{Data: data})
	if err != nil {
		t.Fatalf("attachmentBlob: %v", err)
	}
	// fakeSigner.Encrypt returns "enc(<plaintext>)-><recipient>"; the plaintext
	// fed in must be the base64 of the raw bytes (NIP-44 operates on text).
	want := "enc(" + base64.StdEncoding.EncodeToString(data) + ")->senderpub"
	if string(blob) != want {
		t.Errorf("server blob = %q, want %q", blob, want)
	}
	if string(blob) == string(data) {
		t.Error("server mode must not upload raw plaintext bytes")
	}
}

func TestAttachmentBlob_ServerEncryptFailurePropagates(t *testing.T) {
	s := newServiceWithSigner(&fakeSigner{pubkey: "senderpub", canEnc: true, failEnc: true})
	_, _, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeServerSide, AttachmentInput{Data: []byte("x")})
	if err == nil {
		t.Error("expected encryption failure to propagate (caller skips the attachment)")
	}
}

// Client mode refuses a readable body, and accepts both the body envelope and
// the older NIP-44 payload so older clients keep working.
func TestCheckClientBodyEncrypted(t *testing.T) {
	envelope, err := os.ReadFile("../encryption/testdata/browser_encrypted_body.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkClientBodyEncrypted(strings.TrimSpace(string(envelope))); err != nil {
		t.Errorf("body envelope refused: %v", err)
	}
	if err := checkClientBodyEncrypted(string(bareNIP44(t))); err != nil {
		t.Errorf("older NIP-44 body refused: %v", err)
	}
	for _, plain := range []string{"Hi Bob, the numbers are 1, 2, 3", ""} {
		if err := checkClientBodyEncrypted(plain); !errors.Is(err, ErrClientBodyNotEncrypted) {
			t.Errorf("plaintext %q: err = %v, want ErrClientBodyNotEncrypted", plain, err)
		}
	}
}
