package encryption

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr/nip44"
)

// testEnvelope builds a structurally valid v1 envelope with real NIP-44
// payloads for the wrapped keys.
func testEnvelope(t *testing.T) map[string]any {
	t.Helper()
	var ck [32]byte
	ck[0] = 9
	wrapped, err := nip44.Encrypt(`{"key":"x","subject":"id"}`, ck)
	if err != nil {
		t.Fatal(err)
	}
	sender := strings.Repeat("a", 64)
	recipient := strings.Repeat("b", 64)
	blob := func(n int) map[string]any {
		return map[string]any{
			"ciphertext": base64.RawURLEncoding.EncodeToString(make([]byte, n)),
			"nonce":      base64.RawURLEncoding.EncodeToString(make([]byte, 24)),
			"algorithm":  "xchacha20-poly1305",
		}
	}
	return map[string]any{
		"v":      1,
		"id":     "3b1f0a5e-1c1a-4b5e-9f0a-2d6c1e7b8a90",
		"sender": sender,
		"keys":   map[string]any{sender: wrapped, recipient: wrapped},
		"meta":   blob(40),
		"data":   blob(1000),
	}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestIsEncryptedAttachment(t *testing.T) {
	if !IsEncryptedAttachment(marshal(t, testEnvelope(t))) {
		t.Fatal("valid envelope refused")
	}

	var ck [32]byte
	bareNIP44, _ := nip44.Encrypt("aGVsbG8=", ck)

	mutate := map[string]func(map[string]any){
		"wrong version":        func(e map[string]any) { e["v"] = 2 },
		"no id":                func(e map[string]any) { e["id"] = "" },
		"bad sender":           func(e map[string]any) { e["sender"] = "npub1notahexkey" },
		"no sender envelope":   func(e map[string]any) { delete(e["keys"].(map[string]any), strings.Repeat("a", 64)) },
		"only sender envelope": func(e map[string]any) { delete(e["keys"].(map[string]any), strings.Repeat("b", 64)) },
		"key not NIP-44": func(e map[string]any) {
			e["keys"].(map[string]any)[strings.Repeat("b", 64)] = "plaintext-content-key"
		},
		"data missing":     func(e map[string]any) { delete(e, "data") },
		"wrong algorithm":  func(e map[string]any) { e["data"].(map[string]any)["algorithm"] = "none" },
		"short nonce":      func(e map[string]any) { e["data"].(map[string]any)["nonce"] = "AAAA" },
		"truncated cipher": func(e map[string]any) { e["data"].(map[string]any)["ciphertext"] = "AAAA" },
		// A field outside the format is refused rather than ignored, so the
		// file can't ride along in the clear next to a valid envelope.
		"plaintext smuggled": func(e map[string]any) { e["file"] = base64.StdEncoding.EncodeToString([]byte("secret")) },
	}
	for name, m := range mutate {
		t.Run(name, func(t *testing.T) {
			e := testEnvelope(t)
			m(e)
			if IsEncryptedAttachment(marshal(t, e)) {
				t.Error("accepted")
			}
		})
	}

	for name, b := range map[string][]byte{
		"raw file":                   []byte("%PDF-1.7 plaintext"),
		"empty":                      nil,
		"bare NIP-44 (old stopgap)":  []byte(bareNIP44),
		"two envelopes concatenated": append(marshal(t, testEnvelope(t)), marshal(t, testEnvelope(t))...),
		"json of the plaintext file": []byte(`{"filename":"a.pdf","data":"JVBERi0xLjc="}`),
	} {
		t.Run(name, func(t *testing.T) {
			if IsEncryptedAttachment(b) {
				t.Error("accepted")
			}
		})
	}
}

// The browser builds envelopes with libsodium and nostr-tools; the server
// parses them with Go. This fixture was produced by the UI's own
// encryptAttachment (ui/src/lib/attachment-crypto.ts) so the two cannot drift.
func TestIsEncryptedAttachment_BrowserFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/browser_encrypted_attachment.json")
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncryptedAttachment(b) {
		t.Fatal("envelope produced by the browser code was refused")
	}
}

func TestIsEncryptedBody(t *testing.T) {
	b, err := os.ReadFile("testdata/browser_encrypted_body.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := strings.TrimSpace(string(b))
	if !IsEncryptedBody(fixture) {
		t.Fatal("body envelope produced by the browser code was refused")
	}

	var env map[string]any
	if err := json.Unmarshal([]byte(fixture), &env); err != nil {
		t.Fatal(err)
	}
	mutate := map[string]func(map[string]any){
		"attachment, not body": func(e map[string]any) { e["type"] = "attachment" },
		"no type":              func(e map[string]any) { delete(e, "type") },
		"no data":              func(e map[string]any) { delete(e, "data") },
		"no sender envelope":   func(e map[string]any) { delete(e["keys"].(map[string]any), e["sender"].(string)) },
		"plaintext alongside":  func(e map[string]any) { e["text"] = "the body in the clear" },
	}
	for name, m := range mutate {
		t.Run(name, func(t *testing.T) {
			var e map[string]any
			_ = json.Unmarshal([]byte(fixture), &e)
			m(e)
			if IsEncryptedBody(string(marshal(t, e))) {
				t.Error("accepted")
			}
		})
	}
	for name, s := range map[string]string{
		"plaintext":           "Hi Bob, the numbers are 1, 2, 3",
		"attachment envelope": string(marshal(t, testEnvelope(t))),
		"empty":               "",
	} {
		t.Run(name, func(t *testing.T) {
			if IsEncryptedBody(s) {
				t.Error("accepted")
			}
		})
	}
}
