package email

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/nbd-wtf/go-nostr/nip44"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
)

func TestAttachmentBlob_NonePlaintext(t *testing.T) {
	s := newServiceWithSigner(nil)
	data := []byte{0x00, 0x01, 0x02, 0xff, 'h', 'i'}
	blob, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeNone, AttachmentInput{Data: data})
	if err != nil {
		t.Fatalf("attachmentBlob: %v", err)
	}
	if string(blob) != string(data) {
		t.Errorf("none mode should upload raw bytes, got %v", blob)
	}
}

// nip44Ciphertext returns a real NIP-44 v2 payload of plaintext, as the
// browser extension would produce it.
func nip44Ciphertext(t *testing.T, plaintext string) []byte {
	t.Helper()
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}
	ct, err := nip44.Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("nip44.Encrypt: %v", err)
	}
	return []byte(ct)
}

func TestAttachmentBlob_ClientPassThrough(t *testing.T) {
	s := newServiceWithSigner(nil)
	ciphertext := nip44Ciphertext(t, base64.StdEncoding.EncodeToString([]byte("file bytes")))
	blob, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeClientSide, AttachmentInput{Data: ciphertext})
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
	} {
		t.Run(name, func(t *testing.T) {
			blob, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeClientSide, AttachmentInput{Filename: "f", Data: data})
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
	good := AttachmentInput{Filename: "ok.pdf", Data: nip44Ciphertext(t, "c2VjcmV0")}
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
	blob, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeServerSide, AttachmentInput{Data: data})
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
	_, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeServerSide, AttachmentInput{Data: []byte("x")})
	if err == nil {
		t.Error("expected encryption failure to propagate (caller skips the attachment)")
	}
}
