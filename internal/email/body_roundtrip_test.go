package email

import (
	"context"
	"strings"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/transport"
	"go.uber.org/zap"
)

// What a Cloistr sender writes must read back unchanged at a Cloistr
// recipient. Plaintext-only bodies went out base64 and the inbound
// single-part path never decoded them (the recipient saw base64), and the
// multipart/alternative parts were declared quoted-printable but written raw,
// so '=' sequences were mangled and long lines exceeded RFC 5322's 998.
func TestBodyRoundTrip_SenderToInbound(t *testing.T) {
	tr, err := transport.NewSMTPTransport(&transport.SMTPConfig{Host: "localhost", Port: 25}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	p := &InboundProcessor{logger: zap.NewNop()}
	eqHeavy := "a=b =3D =20 == x= \n=\nprice=100%=ok"
	long := strings.Repeat("0123456789", 200) // 2000 chars, one line
	nonASCII := "Grüße — 日本語 ✓ café"

	cases := []struct {
		name, body, html string
		attach           bool
	}{
		{name: "plaintext only", body: "Hello Bob,\nsee you at 10."},
		{name: "plaintext '=' heavy", body: eqHeavy},
		{name: "plaintext long line", body: long},
		{name: "plaintext non-ASCII", body: nonASCII},
		{name: "alternative", body: "plain part", html: "<p>html part</p>"},
		{name: "alternative '=' heavy", body: eqHeavy, html: `<a href="x?a=1&b=3D2">=20</a>`},
		{name: "alternative long line", body: long, html: "<p>" + long + "</p>"},
		{name: "alternative non-ASCII", body: nonASCII, html: "<p>" + nonASCII + "</p>"},
		{name: "plaintext with attachment", body: eqHeavy + "\n" + nonASCII, attach: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := &transport.Message{
				FromAddress: "alice@cloistr.xyz",
				ToAddresses: []string{"bob@cloistr.xyz"},
				Subject:     "s",
				Body:        c.body,
				HTMLBody:    c.html,
			}
			if c.attach {
				msg.Attachments = []transport.Attachment{{Filename: "a.txt", ContentType: "text/plain", Data: []byte("x")}}
			}
			raw, err := tr.BuildRawEmail(context.Background(), msg)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(raw), "\r\n") {
				if len(line) > 998 {
					t.Fatalf("raw line %d is %d bytes (RFC 5322 limit 998)", i, len(line))
				}
			}
			parsed, err := p.parseMessage(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := norm(parsed.Body); got != norm(c.body) {
				t.Errorf("body:\n got %q\nwant %q", got, norm(c.body))
			}
			if got := norm(parsed.HTMLBody); got != norm(c.html) {
				t.Errorf("html:\n got %q\nwant %q", got, norm(c.html))
			}
		})
	}
}

// External senders: single-part bodies in base64 or quoted-printable, and a
// non-UTF-8 charset, must reach the recipient decoded.
func TestInboundSinglePart_Decoded(t *testing.T) {
	p := &InboundProcessor{logger: zap.NewNop()}
	msg := func(headers, body string) []byte {
		return []byte("From: x@example.com\r\nTo: bob@cloistr.xyz\r\nSubject: s\r\nMIME-Version: 1.0\r\n" + headers + "\r\n" + body)
	}
	cases := []struct{ name, raw, want string }{
		{"base64", string(msg("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n", "R3LDvMOfZSDinJM=\r\n")), "Grüße ✓"},
		{"quoted-printable", string(msg("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n", "Gr=C3=BC=C3=9Fe a=3Db soft=\r\nbreak\r\n")), "Grüße a=b softbreak"},
		{"latin-1 quoted-printable", string(msg("Content-Type: text/plain; charset=ISO-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n", "caf=E9\r\n")), "café"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parsed, err := p.parseMessage([]byte(c.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got := norm(parsed.Body); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// norm compares bodies across the wire: CRLF and LF are the same line break,
// and a trailing line break is not content.
func norm(s string) string {
	return strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}
