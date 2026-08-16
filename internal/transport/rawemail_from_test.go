package transport

import (
	"bytes"
	"context"
	"net/mail"
	"strings"
	"testing"
)

// Gmail rejected outbound mail with:
//
//	550 5.7.1 ... not RFC 5322 compliant: 'From' header is missing.
//
// which failed every MX and surfaced as a 500 from POST /api/v2/email/send.
// The sender resolved correctly upstream ("Sender validated" logged
// fraiyr@cloistr.xyz), so these pin down where the header is actually lost.

func headerValue(raw, name string) (string, bool) {
	for _, line := range strings.Split(raw, "\r\n") {
		if line == "" {
			break // end of headers
		}
		if strings.HasPrefix(strings.ToLower(line), strings.ToLower(name)+":") {
			return strings.TrimSpace(line[len(name)+1:]), true
		}
	}
	return "", false
}

func TestBuildRawEmailIncludesFrom(t *testing.T) {
	tr := newTestTransport(t)

	raw, err := tr.buildRawEmail(context.Background(), &Message{
		FromAddress: "fraiyr@cloistr.xyz",
		ToAddresses: []string{"someone@gmail.com"},
		Subject:     "hello",
		Body:        "body text",
	})
	if err != nil {
		t.Fatalf("buildRawEmail: %v", err)
	}

	got, ok := headerValue(string(raw), "From")
	if !ok {
		t.Fatalf("no From header at all in:\n%s", firstLines(string(raw), 12))
	}
	if got != "fraiyr@cloistr.xyz" {
		t.Fatalf("From = %q, want fraiyr@cloistr.xyz", got)
	}
}

// The exact condition Gmail complains about: a From header that is present but
// EMPTY reads as missing. If the sender ever resolves to "" upstream we should
// fail loudly here rather than emit a message every MX will reject.
func TestBuildRawEmailRejectsEmptyFrom(t *testing.T) {
	tr := newTestTransport(t)

	_, err := tr.buildRawEmail(context.Background(), &Message{
		ToAddresses: []string{"someone@gmail.com"},
		Subject:     "hello",
		Body:        "body text",
	})
	if err == nil {
		t.Fatal("buildRawEmail accepted an empty FromAddress; every MX will reject the result " +
			"with 550 5.7.1 'From' header is missing")
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\r\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// The message Gmail actually receives is buildRawEmail's output AFTER DKIM
// signing, and DKIM PREPENDS its header. If that prepended header is malformed
// — an unfolded signature, or one carrying a blank line — it terminates the
// header block early and everything below it, From included, becomes body.
// Gmail then reports exactly "'From' header is missing".
//
// This asserts the post-signing message still parses as mail with a From.
func TestSignedMessageStillHasParseableFrom(t *testing.T) {
	tr := newTestTransport(t)

	raw, err := tr.buildRawEmail(context.Background(), &Message{
		FromAddress: "fraiyr@cloistr.xyz",
		ToAddresses: []string{"someone@gmail.com"},
		Subject:     "hello",
		Body:        "body text",
	})
	if err != nil {
		t.Fatalf("buildRawEmail: %v", err)
	}

	key, err := GenerateDKIMKey("cloistr.xyz", "mail", 2048)
	if err != nil {
		t.Fatalf("GenerateDKIMKey: %v", err)
	}
	signer, err := NewDKIMSigner(&DKIMConfig{
		Domain:     "cloistr.xyz",
		Selector:   "mail",
		PrivateKey: key.PrivatePEM,
	})
	if err != nil {
		t.Fatalf("NewDKIMSigner: %v", err)
	}

	signed, err := signer.Sign(raw)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Parse the way a receiving MTA does.
	m, err := mail.ReadMessage(bytes.NewReader(signed))
	if err != nil {
		t.Fatalf("signed message does not parse as RFC 5322: %v\n---\n%s", err, firstLines(string(signed), 15))
	}
	if got := m.Header.Get("From"); got != "fraiyr@cloistr.xyz" {
		t.Fatalf("From after signing = %q, want fraiyr@cloistr.xyz\n---\n%s",
			got, firstLines(string(signed), 15))
	}
}
