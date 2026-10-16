package encryption

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr/nip44"
)

func TestIsNIP44Payload(t *testing.T) {
	var key [32]byte
	key[0] = 7
	short, err := nip44.Encrypt("x", key)
	if err != nil {
		t.Fatal(err)
	}
	long, err := nip44.Encrypt(strings.Repeat("A", nip44.MaxPlaintextSize), key)
	if err != nil {
		t.Fatal(err)
	}

	v1 := make([]byte, nip44MinDecoded)
	v1[0] = 0x01

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"real payload, 1-byte plaintext", short, true},
		{"real payload, max plaintext", long, true},
		{"empty", "", false},
		{"plaintext text", "hello, this is not encrypted at all", false},
		{"base64 of plaintext", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("plaintext file ", 20))), false},
		{"wrong version byte", base64.StdEncoding.EncodeToString(v1), false},
		{"not base64", strings.Repeat("!", 200), false},
		{"over the size bound", short + strings.Repeat("A", nip44MaxEncodeLen), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsNIP44Payload(c.in); got != c.want {
				t.Errorf("IsNIP44Payload = %v, want %v", got, c.want)
			}
		})
	}
}
