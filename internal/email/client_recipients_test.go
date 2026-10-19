package email

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
)

// withKeyFor returns the fixture envelope with an extra wrapped key for pk.
// The wrapped value is copied from an existing entry: the server checks shape
// and WHO holds a key, never that it unwraps.
func withKeyFor(t *testing.T, path string, pks ...string) (env []byte, sender, recipient string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	sender = m["sender"].(string)
	keys := m["keys"].(map[string]any)
	var sample any
	for pk, v := range keys {
		sample = v
		if pk != sender {
			recipient = pk
		}
	}
	for _, pk := range pks {
		keys[pk] = sample
	}
	env, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return env, sender, recipient
}

const (
	bodyFixture = "../encryption/testdata/browser_encrypted_body.json"
	attFixture  = "../encryption/testdata/browser_encrypted_attachment.json"
	carolPK     = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	davePK      = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

// A client-mode send went out with the body wrapped to To only; CC got the same
// ciphertext and no key. The server now refuses any client-mode send that
// leaves a recipient (To, CC or BCC) without a wrapped key, and names them.
func TestCheckClientRecipientsCovered(t *testing.T) {
	body, _, bob := withKeyFor(t, bodyFixture)
	bodyCarol, _, _ := withKeyFor(t, bodyFixture, carolPK)
	bodyCarolDave, _, _ := withKeyFor(t, bodyFixture, carolPK, davePK)

	t.Run("To only, covered", func(t *testing.T) {
		got, err := checkClientRecipientsCovered(&SendRequest{
			To: []string{"bob@x"}, PreEncryptedBody: string(body),
			RecipientPubkeys: map[string]string{"bob@x": bob},
		})
		if err != nil || !got["bob@x"] || len(got) != 1 {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("address case and spacing do not cause a false refusal", func(t *testing.T) {
		got, err := checkClientRecipientsCovered(&SendRequest{
			To: []string{"Bob@X"}, CC: []string{" carol@x"}, PreEncryptedBody: string(bodyCarol),
			RecipientPubkeys: map[string]string{"bob@x": strings.ToUpper(bob), "Carol@X": carolPK},
		})
		if err != nil || !got["Bob@X"] || !got[" carol@x"] {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("CC with no pubkey is refused and named", func(t *testing.T) {
		_, err := checkClientRecipientsCovered(&SendRequest{
			To: []string{"bob@x"}, CC: []string{"carol@x"}, PreEncryptedBody: string(body),
			RecipientPubkeys: map[string]string{"bob@x": bob},
		})
		if !errors.Is(err, ErrClientRecipientNotEncrypted) || !strings.Contains(err.Error(), "carol@x") {
			t.Fatalf("err = %v, want ErrClientRecipientNotEncrypted naming carol@x", err)
		}
	})

	t.Run("CC pubkey known but no key wrapped to it is refused", func(t *testing.T) {
		// Exactly the shipped bug: ComposePage wrapped to To only.
		_, err := checkClientRecipientsCovered(&SendRequest{
			To: []string{"bob@x"}, CC: []string{"carol@x"}, PreEncryptedBody: string(body),
			RecipientPubkeys: map[string]string{"bob@x": bob, "carol@x": carolPK},
		})
		if !errors.Is(err, ErrClientRecipientNotEncrypted) || !strings.Contains(err.Error(), "carol@x") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("BCC is held to the same rule", func(t *testing.T) {
		_, err := checkClientRecipientsCovered(&SendRequest{
			To: []string{"bob@x"}, CC: []string{"carol@x"}, BCC: []string{"dave@x"},
			PreEncryptedBody: string(bodyCarol),
			RecipientPubkeys: map[string]string{"bob@x": bob, "carol@x": carolPK, "dave@x": davePK},
		})
		if !errors.Is(err, ErrClientRecipientNotEncrypted) || !strings.Contains(err.Error(), "dave@x") ||
			strings.Contains(err.Error(), "carol@x") {
			t.Fatalf("err = %v, want only dave@x named", err)
		}
	})

	t.Run("everyone wrapped in body and attachments passes", func(t *testing.T) {
		att, _, _ := withKeyFor(t, attFixture, bob, carolPK, davePK)
		got, err := checkClientRecipientsCovered(&SendRequest{
			To: []string{"bob@x"}, CC: []string{"carol@x"}, BCC: []string{"dave@x"},
			PreEncryptedBody: string(bodyCarolDave),
			RecipientPubkeys: map[string]string{"bob@x": bob, "carol@x": carolPK, "dave@x": davePK},
			Attachments:      []AttachmentInput{{Filename: "a", Data: att}},
		})
		if err != nil || len(got) != 3 {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("an attachment missing a recipient's key is refused", func(t *testing.T) {
		att, _, _ := withKeyFor(t, attFixture, bob) // no carol
		_, err := checkClientRecipientsCovered(&SendRequest{
			To: []string{"bob@x"}, CC: []string{"carol@x"},
			PreEncryptedBody: string(bodyCarol),
			RecipientPubkeys: map[string]string{"bob@x": bob, "carol@x": carolPK},
			Attachments:      []AttachmentInput{{Filename: "a", Data: att}},
		})
		if !errors.Is(err, ErrClientRecipientNotEncrypted) || !strings.Contains(err.Error(), "carol@x") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("older single-recipient NIP-44 body: one recipient only", func(t *testing.T) {
		legacy := string(bareNIP44(t))
		if _, err := checkClientRecipientsCovered(&SendRequest{
			To: []string{"bob@x"}, PreEncryptedBody: legacy,
			RecipientPubkeys: map[string]string{"bob@x": bob},
		}); err != nil {
			t.Fatalf("single recipient legacy refused: %v", err)
		}
		if _, err := checkClientRecipientsCovered(&SendRequest{
			To: []string{"bob@x"}, CC: []string{"carol@x"}, PreEncryptedBody: legacy,
			RecipientPubkeys: map[string]string{"bob@x": bob, "carol@x": carolPK},
		}); !errors.Is(err, ErrClientRecipientNotEncrypted) {
			t.Fatalf("legacy body with CC: err = %v", err)
		}
	})

	_ = encryption.ModeClientSide
}
