// Package blobseal encrypts files the server stores on Blossom.
//
// Blossom serves blobs to anyone who knows the hash (GET is unauthenticated),
// so anything private must be ciphertext before upload. Each blob gets its own
// random key (never derived from anything), and that key is stored wrapped
// under the server's key-encryption key (KEK) next to the attachment row.
// Rotating the KEK rewraps the stored keys and never touches the blobs.
//
// Both layers are XChaCha20-Poly1305 with a random 24-byte nonce, so a tampered
// blob, a tampered key, the wrong KEK or another blob's key fails to open
// instead of returning wrong bytes.
package blobseal

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

// KEKSize is the key-encryption key length in bytes.
const KEKSize = chacha20poly1305.KeySize

// keyPrefix versions the wrapped-key format, so a future format or KEK can be
// told apart from this one.
const keyPrefix = "v1."

// Associated data binds each ciphertext to its role: a wrapped key cannot be
// passed off as a blob or the other way round.
var (
	blobAAD = []byte("cloistr-email/attachment-blob/v1")
	keyAAD  = []byte("cloistr-email/attachment-key/v1")
)

// ErrOpen is returned for every failure to open a sealed blob. Callers must
// treat it as "no file", never fall back to serving the stored bytes.
var ErrOpen = errors.New("sealed blob could not be opened")

// Sealer seals and opens blobs under one KEK.
type Sealer struct {
	kek []byte
}

// NewSealer builds a Sealer from a base64-encoded 32-byte KEK.
func NewSealer(kekBase64 string) (*Sealer, error) {
	kek, err := base64.StdEncoding.DecodeString(strings.TrimSpace(kekBase64))
	if err != nil {
		return nil, errors.New("attachment KEK is not valid base64")
	}
	if len(kek) != KEKSize {
		return nil, fmt.Errorf("attachment KEK must be %d bytes, got %d", KEKSize, len(kek))
	}
	if subtle.ConstantTimeCompare(kek, make([]byte, KEKSize)) == 1 {
		return nil, errors.New("attachment KEK is all zero")
	}
	return &Sealer{kek: kek}, nil
}

// Seal encrypts plaintext under a fresh random key. It returns the blob to
// upload and the key wrapped under the KEK, to store with the attachment row.
func (s *Sealer) Seal(plaintext []byte) (blob []byte, wrappedKey string, err error) {
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, "", fmt.Errorf("generate blob key: %w", err)
	}
	blob, err = seal(key, plaintext, blobAAD)
	if err != nil {
		return nil, "", err
	}
	wrapped, err := seal(s.kek, key, keyAAD)
	if err != nil {
		return nil, "", err
	}
	return blob, keyPrefix + base64.RawURLEncoding.EncodeToString(wrapped), nil
}

// Open unwraps wrappedKey and decrypts blob. Every failure is ErrOpen and
// returns no bytes.
func (s *Sealer) Open(wrappedKey string, blob []byte) ([]byte, error) {
	if !strings.HasPrefix(wrappedKey, keyPrefix) {
		return nil, fmt.Errorf("%w: missing or unknown key format", ErrOpen)
	}
	wrapped, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(wrappedKey, keyPrefix))
	if err != nil {
		return nil, fmt.Errorf("%w: malformed key", ErrOpen)
	}
	key, err := open(s.kek, wrapped, keyAAD)
	if err != nil {
		return nil, fmt.Errorf("%w: key does not unwrap", ErrOpen)
	}
	plaintext, err := open(key, blob, blobAAD)
	if err != nil {
		return nil, fmt.Errorf("%w: blob does not decrypt", ErrOpen)
	}
	return plaintext, nil
}

// seal returns nonce || ciphertext.
func seal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plaintext)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

func open(key, sealed, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	return aead.Open(nil, nonce, ct, aad)
}
