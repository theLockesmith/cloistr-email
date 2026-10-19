package encryption

import (
	"encoding/json"
	"os"
	"testing"
)

func fixtureKeys(t *testing.T, path string) (sender string, keys map[string]string, raw []byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Sender string            `json:"sender"`
		Keys   map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	return env.Sender, env.Keys, raw
}

// The server needs to know WHO an envelope's content key is wrapped to, not
// just that it is an envelope: a CC recipient with no wrapped key receives a
// message they cannot open.
func TestBodyKeyHolders(t *testing.T) {
	sender, keys, raw := fixtureKeys(t, "testdata/browser_encrypted_body.json")
	got, ok := BodyKeyHolders(string(raw))
	if !ok {
		t.Fatal("browser body envelope not recognised")
	}
	if len(got) != len(keys) || !got[sender] {
		t.Fatalf("holders = %v, want the %d wrapped keys incl. sender", got, len(keys))
	}
	for pk := range keys {
		if !got[pk] {
			t.Errorf("missing holder %s", pk)
		}
	}
	if _, ok := BodyKeyHolders("not an envelope"); ok {
		t.Error("plaintext reported as an envelope")
	}
}

func TestAttachmentKeyHolders(t *testing.T) {
	sender, keys, raw := fixtureKeys(t, "testdata/browser_encrypted_attachment.json")
	got, ok := AttachmentKeyHolders(raw)
	if !ok {
		t.Fatal("browser attachment envelope not recognised")
	}
	if len(got) != len(keys) || !got[sender] {
		t.Fatalf("holders = %v, want the %d wrapped keys incl. sender", got, len(keys))
	}
	if _, ok := AttachmentKeyHolders([]byte("%PDF-1.7")); ok {
		t.Error("plaintext reported as an envelope")
	}
}
