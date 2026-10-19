package transport

import (
	"context"
	"strings"
	"testing"
)

// headerBlock returns the raw message's header section (up to the first blank
// line) and its body.
func headerBlock(raw []byte) (string, string) {
	s := string(raw)
	i := strings.Index(s, "\r\n\r\n")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+4:]
}

func countHeader(hdr, name string) int {
	n := 0
	for _, line := range strings.Split(hdr, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), strings.ToLower(name)+":") {
			n++
		}
	}
	return n
}

// Defence in depth behind the API check: whatever reaches the transport, a
// value can never start a new header line or end the header block.
func TestBuildRawEmail_HeaderInjectionNeutralised(t *testing.T) {
	tr := newTestTransport(t)
	raw, err := tr.buildRawEmail(context.Background(), &Message{
		FromAddress: "alice@cloistr.xyz",
		ToAddresses: []string{"bob@example.com"},
		Subject:     "hi\r\nFrom: ceo@bank.example\r\nReply-To: attacker@evil.example\r\nBcc: extra@evil.example",
		InReplyTo:   "x@y>\r\nX-Injected: via-in-reply-to\r\nZ: <z",
		References:  []string{"r@s>\nX-Ref-Injected: 1\n<t"},
		Body:        "real body",
	})
	if err != nil {
		t.Fatal(err)
	}
	hdr, _ := headerBlock(raw)
	if n := countHeader(hdr, "From"); n != 1 {
		t.Errorf("From headers = %d, want 1\n%s", n, hdr)
	}
	for _, h := range []string{"Reply-To", "Bcc", "X-Injected", "X-Ref-Injected"} {
		if countHeader(hdr, h) != 0 {
			t.Errorf("injected %s header present\n%s", h, hdr)
		}
	}
}

func TestBuildRawEmail_BodyReplacementNeutralised(t *testing.T) {
	tr := newTestTransport(t)
	raw, err := tr.buildRawEmail(context.Background(), &Message{
		FromAddress: "alice@cloistr.xyz",
		ToAddresses: []string{"bob@example.com"},
		Subject:     "hi\r\n\r\nATTACKER BODY",
		Body:        "real body",
	})
	if err != nil {
		t.Fatal(err)
	}
	hdr, body := headerBlock(raw)
	if strings.Contains(body, "ATTACKER BODY") && !strings.Contains(hdr, "ATTACKER BODY") {
		t.Errorf("subject ended the header block and injected a body:\n%s", raw)
	}
	if countHeader(hdr, "MIME-Version") != 1 || countHeader(hdr, "Content-Type") != 1 {
		t.Errorf("header block cut short by the subject:\n%s", hdr)
	}
}

func TestBuildRawEmail_AttachmentHeaderInjectionNeutralised(t *testing.T) {
	tr := newTestTransport(t)
	raw, err := tr.buildRawEmail(context.Background(), &Message{
		FromAddress: "alice@cloistr.xyz",
		ToAddresses: []string{"bob@example.com"},
		Subject:     "hi",
		Body:        "real body",
		Attachments: []Attachment{{Filename: "a.txt\"\r\nX-Att-Injected: 1\r\nZ: \"", ContentType: "text/plain\r\nX-Ct-Injected: 1", Data: []byte("x")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if strings.HasPrefix(line, "X-Att-Injected:") || strings.HasPrefix(line, "X-Ct-Injected:") {
			t.Errorf("injected attachment header line: %q", line)
		}
	}
}
