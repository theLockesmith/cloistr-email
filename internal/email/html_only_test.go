package email

import (
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"go.uber.org/zap"
)

// An HTML-only inbound message was stored with its HTML (html_body) but the
// read path never returned it: GetEmailResult had no HTML field, so the API
// sent no html_body and the reader saw an empty message. Raw message ->
// inbound parse -> stored record -> read result must carry the content.
func TestHTMLOnlyInbound_RoundTrips(t *testing.T) {
	const html = `<p>Quarterly <b>numbers</b> attached &amp; approved.</p>`
	raw := []byte("From: x@example.com\r\nTo: bob@cloistr.xyz\r\nSubject: s\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"<p>Quarterly <b>numbers</b> attached &amp; approved.</p>\r\n")

	p := &InboundProcessor{logger: zap.NewNop()}
	parsed, err := p.parseMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	rec := receivedRecord(parsed, "bob@cloistr.xyz", "pubkey", nil)
	if rec.HTMLBody == nil || norm(*rec.HTMLBody) != html {
		t.Fatalf("stored html_body = %v", rec.HTMLBody)
	}
	if got := norm(htmlBodyFor(rec, encryption.ModeNone)); got != html {
		t.Errorf("read path html = %q, want %q", got, html)
	}
	// Never hand out stored HTML for an encrypted message.
	rec.IsEncrypted = true
	if got := htmlBodyFor(rec, encryption.ModeClientSide); got != "" {
		t.Errorf("encrypted message returned html %q", got)
	}
}
