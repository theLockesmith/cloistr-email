package blossom

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// clientUploadAuth is what the browser produces: a kind-24242 upload
// authorization for one blob, signed with the user's own key.
func clientUploadAuth(t *testing.T, sk string, hash string, exp time.Time, mutate func(*nostr.Event)) *nostr.Event {
	t.Helper()
	pk, _ := nostr.GetPublicKey(sk)
	ev := buildAuthEvent(pk, "upload", []string{hash}, exp)
	if mutate != nil {
		mutate(ev)
	}
	if err := ev.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestValidateUploadAuth(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pk, _ := nostr.GetPublicKey(sk)
	other := nostr.GeneratePrivateKey()
	blob := []byte("ciphertext envelope bytes")
	hash := BlobHash(blob)
	now := time.Now()
	later := now.Add(10 * time.Minute)

	if err := ValidateUploadAuth(clientUploadAuth(t, sk, hash, later, nil), pk, hash, now); err != nil {
		t.Fatalf("valid authorization refused: %v", err)
	}

	tampered := clientUploadAuth(t, sk, hash, later, nil)
	tampered.Tags = append(tampered.Tags, nostr.Tag{"x", strings.Repeat("0", 64)})

	cases := map[string]*nostr.Event{
		"nil":                    nil,
		"signed by someone else": clientUploadAuth(t, other, hash, later, nil),
		"tampered after signing": tampered,
		"other blob":             clientUploadAuth(t, sk, BlobHash([]byte("different")), later, nil),
		"expired":                clientUploadAuth(t, sk, hash, now.Add(-time.Minute), nil),
		"delete, not upload": clientUploadAuth(t, sk, hash, later, func(e *nostr.Event) {
			e.Tags[0] = nostr.Tag{"t", "delete"}
		}),
		"no expiration": clientUploadAuth(t, sk, hash, later, func(e *nostr.Event) {
			e.Tags = nostr.Tags{{"t", "upload"}, {"x", hash}}
		}),
		"wrong kind": clientUploadAuth(t, sk, hash, later, func(e *nostr.Event) { e.Kind = 27235 }),
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) {
			if ValidateUploadAuth(ev, pk, hash, now) == nil {
				t.Error("accepted")
			}
		})
	}
}

// The server uploads with the client's own authorization, unchanged, and that
// authorization cannot be reused for anything but the blob it names.
func TestPresignedUploadAuth(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	blob := []byte("ciphertext envelope bytes")
	hash := BlobHash(blob)
	ev := clientUploadAuth(t, sk, hash, time.Now().Add(10*time.Minute), nil)

	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"sha256":%q}`, hash)
	}))
	defer ts.Close()

	signer := NewPresignedUploadAuth(ev, hash)
	if _, err := NewClient(signer, nil).Upload(context.Background(), blob, "application/octet-stream", []Server{{URL: ts.URL}}, 1); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(gotAuth, "Nostr "))
	if err != nil {
		t.Fatal(err)
	}
	var sent nostr.Event
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.ID != ev.ID || sent.Sig != ev.Sig {
		t.Error("Blossom did not receive the client's own signed authorization")
	}

	if _, err := signer.SignAuth(context.Background(), "upload", []string{BlobHash([]byte("x"))}, time.Now()); err == nil {
		t.Error("presigned authorization was reused for another blob")
	}
	if _, err := signer.SignAuth(context.Background(), "delete", []string{hash}, time.Now()); err == nil {
		t.Error("presigned authorization was reused for delete")
	}
}
