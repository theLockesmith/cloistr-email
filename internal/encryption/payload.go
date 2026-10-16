package encryption

import "encoding/base64"

// NIP-44 v2 payload bounds, decoded: version(1) + nonce(32) + padded
// ciphertext (2-byte length prefix + at least 32 bytes) + MAC(32), up to a
// 65535-byte plaintext padded to 65536.
const (
	nip44V2Version    = 0x02
	nip44MinDecoded   = 1 + 32 + 2 + 32 + 32
	nip44MaxDecoded   = 1 + 32 + 2 + 65536 + 32
	nip44MinEncodeLen = 132
	nip44MaxEncodeLen = 87472
)

// IsNIP44Payload reports whether s has the shape of a NIP-44 v2 payload:
// standard base64 whose decoded bytes start with the v2 version byte and fall
// within the spec's length bounds. It cannot prove the payload decrypts (that
// needs the conversation key); it is the server-side check that a client
// claiming end-to-end encryption actually sent ciphertext and not plaintext.
func IsNIP44Payload(s string) bool {
	if len(s) < nip44MinEncodeLen || len(s) > nip44MaxEncodeLen {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return false
	}
	if len(raw) < nip44MinDecoded || len(raw) > nip44MaxDecoded {
		return false
	}
	return raw[0] == nip44V2Version
}
