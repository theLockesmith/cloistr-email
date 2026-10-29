package email

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/blossom"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
)

// serverAuthSpy stands in for the server-held signer and records any use.
type serverAuthSpy struct{ used bool }

func (s *serverAuthSpy) SignAuth(context.Context, string, []string, time.Time) (*nostr.Event, error) {
	s.used = true
	return &nostr.Event{}, nil
}
func (s *serverAuthSpy) PublicKey() string { return "server" }

// signedUpload is a client's own kind-24242 upload authorization for data.
func signedUpload(t *testing.T, sk string, data []byte) *nostr.Event {
	t.Helper()
	pk, _ := nostr.GetPublicKey(sk)
	ev := &nostr.Event{
		PubKey:    pk,
		CreatedAt: nostr.Now(),
		Kind:      24242,
		Tags: nostr.Tags{
			{"t", "upload"},
			{"x", blossom.BlobHash(data)},
			{"expiration", "9999999999"},
		},
		Content: "upload blob",
	}
	if err := ev.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return ev
}

// Client-side mode: the server never signs for the user. With the sender's
// own authorization the attachment is stored under it; without one it is not
// stored at all (it was still delivered by mail).
func TestUploadAuthFor(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	blob := clientEnvelope(t)

	spy := &serverAuthSpy{}
	if _, ok := uploadAuthFor(encryption.ModeClientSide, spy, nil, AttachmentInput{Data: blob}, blob); ok {
		t.Error("client mode without the sender's authorization must not upload")
	}

	auth := signedUpload(t, sk, blob)
	signer, ok := uploadAuthFor(encryption.ModeClientSide, spy, nil, AttachmentInput{Data: blob, UploadAuth: auth}, blob)
	if !ok {
		t.Fatal("client mode with the sender's authorization was not uploaded")
	}
	ev, err := signer.SignAuth(context.Background(), "upload", []string{blossom.BlobHash(blob)}, time.Now())
	if err != nil || ev.ID != auth.ID {
		t.Errorf("upload not authorized by the sender's own event: %v", err)
	}
	if spy.used {
		t.Error("the server signer was used in client mode")
	}

	if s, ok := uploadAuthFor(encryption.ModeServerSide, spy, nil, AttachmentInput{Data: blob}, blob); !ok || s != spy {
		t.Error("server mode should keep using the server signer")
	}

	// Unencrypted mode: the blob is sealed by the server and uploaded under
	// Mail's service key, never the user's session; without one, no upload.
	service := &serverAuthSpy{}
	if s, ok := uploadAuthFor(encryption.ModeNone, spy, service, AttachmentInput{Data: blob}, blob); !ok || s != service {
		t.Error("unencrypted mode should use the Mail service key")
	}
	if _, ok := uploadAuthFor(encryption.ModeNone, spy, nil, AttachmentInput{Data: blob}, blob); ok {
		t.Error("unencrypted mode without a service key must not upload")
	}
}

func TestCheckClientUploadAuth(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	blob := clientEnvelope(t)
	now := time.Now()

	if err := checkClientUploadAuth(pk, []AttachmentInput{{Data: blob}}, now); err != nil {
		t.Errorf("no authorization is allowed (mail-only): %v", err)
	}
	if err := checkClientUploadAuth(pk, []AttachmentInput{{Data: blob, UploadAuth: signedUpload(t, sk, blob)}}, now); err != nil {
		t.Errorf("valid authorization refused: %v", err)
	}
	someoneElse := signedUpload(t, nostr.GeneratePrivateKey(), blob)
	if err := checkClientUploadAuth(pk, []AttachmentInput{{Filename: "f", Data: blob, UploadAuth: someoneElse}}, now); !errors.Is(err, ErrInvalidUploadAuth) {
		t.Errorf("another key's authorization: err = %v, want ErrInvalidUploadAuth", err)
	}
	otherBlob := signedUpload(t, sk, []byte("not these bytes"))
	if err := checkClientUploadAuth(pk, []AttachmentInput{{Filename: "f", Data: blob, UploadAuth: otherBlob}}, now); !errors.Is(err, ErrInvalidUploadAuth) {
		t.Errorf("authorization for other bytes: err = %v, want ErrInvalidUploadAuth", err)
	}
}
