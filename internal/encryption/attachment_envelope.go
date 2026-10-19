package encryption

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// EncryptedAttachmentContentType is the content type a client sends for every
// client-side encrypted attachment; the real type is inside the envelope.
const EncryptedAttachmentContentType = "application/vnd.cloistr.encrypted-attachment+json"

// encryptedAttachmentV1 is the wire shape built by the UI's attachment-crypto.ts:
// a per-attachment content key encrypts the file and its name/type
// (XChaCha20-Poly1305, @cloistr/collab-common/storage), and that key is wrapped
// to each recipient and to the sender with the shared envelope primitive
// (NIP-44 v2, @cloistr/auth). See architecture/key-wrapping-and-collection-keys.md.
type encryptedAttachmentV1 struct {
	V      int               `json:"v"`
	ID     string            `json:"id"`
	Sender string            `json:"sender"`
	Keys   map[string]string `json:"keys"`
	Meta   *sealedBlob       `json:"meta"`
	Data   *sealedBlob       `json:"data"`
}

type sealedBlob struct {
	Ciphertext string `json:"ciphertext"`
	Nonce      string `json:"nonce"`
	Algorithm  string `json:"algorithm"`
}

// XChaCha20-Poly1305: 24-byte nonce, 16-byte tag.
const (
	xchachaNonceBytes = 24
	poly1305TagBytes  = 16
	maxEnvelopeIDLen  = 128
)

// IsEncryptedAttachment reports whether b has the shape of a client-encrypted
// attachment: a version-1 envelope whose file and metadata are sealed blobs and
// whose content key is wrapped (as NIP-44 v2 payloads) to at least one
// recipient and to the sender. Like IsNIP44Payload it cannot prove the content
// decrypts; it is the server's check that a client claiming end-to-end
// encryption sent ciphertext rather than a readable file.
func IsEncryptedAttachment(b []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var env encryptedAttachmentV1
	if err := dec.Decode(&env); err != nil || dec.More() {
		return false
	}
	if env.V != 1 || !validKeyEnvelopes(env.ID, env.Sender, env.Keys) {
		return false
	}
	return isSealedBlob(env.Meta) && isSealedBlob(env.Data)
}

// encryptedBodyV1 is the body envelope built by the UI's body-crypto.ts: the
// same model as an attachment, with the body (and its protected headers) as
// the only sealed blob.
type encryptedBodyV1 struct {
	V      int               `json:"v"`
	Type   string            `json:"type"`
	ID     string            `json:"id"`
	Sender string            `json:"sender"`
	Keys   map[string]string `json:"keys"`
	Data   *sealedBlob       `json:"data"`
}

// IsEncryptedBody reports whether s has the shape of a client-encrypted body
// envelope. Bodies in the older format (a NIP-44 payload to the recipient)
// are checked with IsNIP44Payload instead.
func IsEncryptedBody(s string) bool {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	var env encryptedBodyV1
	if err := dec.Decode(&env); err != nil || dec.More() {
		return false
	}
	return env.V == 1 && env.Type == "body" && validKeyEnvelopes(env.ID, env.Sender, env.Keys) && isSealedBlob(env.Data)
}

// BodyKeyHolders returns the pubkeys a client-encrypted body envelope's content
// key is wrapped to (the sender included), and false if s is not a body
// envelope. A recipient missing from this set receives a body they cannot open.
func BodyKeyHolders(s string) (map[string]bool, bool) {
	if !IsEncryptedBody(s) {
		return nil, false
	}
	var env encryptedBodyV1
	if err := json.Unmarshal([]byte(s), &env); err != nil {
		return nil, false
	}
	return keyHolders(env.Keys), true
}

// AttachmentKeyHolders is BodyKeyHolders for an attachment envelope.
func AttachmentKeyHolders(b []byte) (map[string]bool, bool) {
	if !IsEncryptedAttachment(b) {
		return nil, false
	}
	var env encryptedAttachmentV1
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, false
	}
	return keyHolders(env.Keys), true
}

func keyHolders(keys map[string]string) map[string]bool {
	out := make(map[string]bool, len(keys))
	for pk := range keys {
		out[strings.ToLower(pk)] = true
	}
	return out
}

// validKeyEnvelopes checks the key-wrapping half shared by both envelopes: an
// id (the envelope subject), the sender, the sender's own wrapped key plus at
// least one other, and every wrapped key a NIP-44 v2 payload.
func validKeyEnvelopes(id, sender string, keys map[string]string) bool {
	if id == "" || len(id) > maxEnvelopeIDLen || !isHexPubkey(sender) {
		return false
	}
	if _, ok := keys[sender]; !ok || len(keys) < 2 {
		return false
	}
	for pk, wrapped := range keys {
		if !isHexPubkey(pk) || !IsNIP44Payload(wrapped) {
			return false
		}
	}
	return true
}

func isSealedBlob(s *sealedBlob) bool {
	if s == nil || s.Algorithm != "xchacha20-poly1305" {
		return false
	}
	nonce, err := decodeSodiumBase64(s.Nonce)
	if err != nil || len(nonce) != xchachaNonceBytes {
		return false
	}
	ct, err := decodeSodiumBase64(s.Ciphertext)
	return err == nil && len(ct) >= poly1305TagBytes
}

// decodeSodiumBase64 reads libsodium's default base64 variant (URL-safe, no
// padding), which is what the blob cipher emits.
func decodeSodiumBase64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func isHexPubkey(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
