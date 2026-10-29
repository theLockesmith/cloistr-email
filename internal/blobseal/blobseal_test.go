package blobseal

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testKEK(t *testing.T) string {
	t.Helper()
	k := make([]byte, KEKSize)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func mustSealer(t *testing.T, kek string) *Sealer {
	t.Helper()
	s, err := NewSealer(kek)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

var plaintext = []byte("%PDF-1.4 quarterly figures: confidential\x00\xff")

func TestSealedBlobIsNotThePlaintext(t *testing.T) {
	s := mustSealer(t, testKEK(t))
	blob, key, err := s.Seal(plaintext, "row-1")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(blob, plaintext) || bytes.Contains(blob, []byte("confidential")) {
		t.Fatal("sealed blob contains the plaintext")
	}
	if key == "" || strings.Contains(key, string(plaintext)) {
		t.Fatalf("wrapped key = %q", key)
	}
}

func TestRoundTrip(t *testing.T) {
	s := mustSealer(t, testKEK(t))
	blob, key, err := s.Seal(plaintext, "row-1")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	got, err := s.Open(key, blob, "row-1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip = %q, want %q", got, plaintext)
	}
}

// Keys are random per blob, never derived: sealing the same file twice gives
// different keys and different blobs (so equal files are not linkable by hash).
func TestKeysAreRandomPerBlob(t *testing.T) {
	s := mustSealer(t, testKEK(t))
	b1, k1, _ := s.Seal(plaintext, "row-1")
	b2, k2, _ := s.Seal(plaintext, "row-1")
	if k1 == k2 || bytes.Equal(b1, b2) {
		t.Fatal("two seals of the same plaintext produced the same key or blob")
	}
}

func TestFailsClosed(t *testing.T) {
	s := mustSealer(t, testKEK(t))
	other := mustSealer(t, testKEK(t))
	blob, key, err := s.Seal(plaintext, "row-1")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	_, otherKey, _ := s.Seal([]byte("another file"), "row-1")

	flip := func(b []byte, i int) []byte {
		c := append([]byte(nil), b...)
		c[i] ^= 0x01
		return c
	}
	rawKey, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, keyPrefix))
	tamperedKey := keyPrefix + base64.RawURLEncoding.EncodeToString(flip(rawKey, len(rawKey)-1))

	for name, open := range map[string]func() ([]byte, error){
		"missing key":         func() ([]byte, error) { return s.Open("", blob, "row-1") },
		"malformed key":       func() ([]byte, error) { return s.Open("v1.not-base64!!", blob, "row-1") },
		"unknown key version": func() ([]byte, error) { return s.Open("v9."+strings.TrimPrefix(key, keyPrefix), blob, "row-1") },
		"wrong KEK":           func() ([]byte, error) { return other.Open(key, blob, "row-1") },
		"another blob's key":  func() ([]byte, error) { return s.Open(otherKey, blob, "row-1") },
		"tampered key":        func() ([]byte, error) { return s.Open(tamperedKey, blob, "row-1") },
		"tampered blob":       func() ([]byte, error) { return s.Open(key, flip(blob, len(blob)-1), "row-1") },
		"truncated blob":      func() ([]byte, error) { return s.Open(key, blob[:10], "row-1") },
		"empty blob":          func() ([]byte, error) { return s.Open(key, nil, "row-1") },
	} {
		got, err := open()
		if err == nil {
			t.Errorf("%s: opened without error", name)
		}
		if !errors.Is(err, ErrOpen) {
			t.Errorf("%s: err = %v, want ErrOpen", name, err)
		}
		if got != nil {
			t.Errorf("%s: returned %d bytes on failure", name, len(got))
		}
	}
}

func TestNewSealerRejectsBadKEK(t *testing.T) {
	for name, kek := range map[string]string{
		"empty":      "",
		"not base64": "!!!",
		"too short":  base64.StdEncoding.EncodeToString(make([]byte, 16)),
		"too long":   base64.StdEncoding.EncodeToString(make([]byte, 64)),
		"all zero":   base64.StdEncoding.EncodeToString(make([]byte, KEKSize)),
	} {
		if _, err := NewSealer(kek); err == nil {
			t.Errorf("%s KEK accepted", name)
		}
	}
}
