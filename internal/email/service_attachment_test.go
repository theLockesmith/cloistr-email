package email

import (
	"context"
	"encoding/base64"
	"testing"

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

func TestAttachmentBlob_ClientPassThrough(t *testing.T) {
	s := newServiceWithSigner(nil)
	ciphertext := []byte("already-encrypted-by-client")
	blob, err := s.attachmentBlob(context.Background(), "senderpub", encryption.ModeClientSide, AttachmentInput{Data: ciphertext})
	if err != nil {
		t.Fatalf("attachmentBlob: %v", err)
	}
	if string(blob) != string(ciphertext) {
		t.Errorf("client mode should pass ciphertext through unchanged")
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
