package email

import (
	"bytes"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// newTestProcessor builds a processor with no DB. Safe because every test here
// exercises only the pure parse path, which never touches storage.
func newTestProcessor() *InboundProcessor {
	return &InboundProcessor{logger: zap.NewNop()}
}

func TestDecodeTransferEncoding(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		encoding string
		want     string
	}{
		{"base64", "aGVsbG8gd29ybGQ=", "base64", "hello world"},
		{
			// Mail base64 is wrapped at 76 columns; the line breaks are not data.
			name:     "base64 with line wrapping",
			raw:      "aGVsbG8g\r\nd29ybGQ=",
			encoding: "base64",
			want:     "hello world",
		},
		{"base64 case-insensitive header", "aGk=", "BASE64", "hi"},
		{
			// This is the visible symptom of the missing decode: a body reading
			// "Hello=20world" instead of "Hello world".
			name:     "quoted-printable",
			raw:      "Hello=20world=21",
			encoding: "quoted-printable",
			want:     "Hello world!",
		},
		{"quoted-printable soft line break", "long=\r\nline", "quoted-printable", "longline"},
		{"7bit passes through", "plain text", "7bit", "plain text"},
		{"empty encoding passes through", "plain text", "", "plain text"},
		{"unknown encoding passes through", "plain text", "x-weird", "plain text"},
		{
			// Malformed input must not lose the message. Returning the raw bytes
			// keeps a slightly-wrong body instead of failing delivery.
			name:     "undecodable base64 falls back to raw",
			raw:      "!!!not base64!!!",
			encoding: "base64",
			want:     "!!!not base64!!!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeTransferEncoding([]byte(tt.raw), tt.encoding)
			if string(got) != tt.want {
				t.Errorf("decodeTransferEncoding(%q, %q) = %q, want %q",
					tt.raw, tt.encoding, got, tt.want)
			}
		})
	}
}

func TestParseMultipartExtractsAttachments(t *testing.T) {
	body := strings.Join([]string{
		"--BOUND",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"See the=20attached file.",
		"--BOUND",
		"Content-Type: application/pdf; name=\"report.pdf\"",
		"Content-Disposition: attachment; filename=\"report.pdf\"",
		"Content-Transfer-Encoding: base64",
		"",
		"JVBERi0xLjQK",
		"--BOUND--",
		"",
	}, "\r\n")

	parsed := &ParsedMessage{}
	p := newTestProcessor()
	if err := p.parseMultipart(bytes.NewReader([]byte(body)), "BOUND", parsed); err != nil {
		t.Fatalf("parseMultipart returned error: %v", err)
	}

	// The body must be DECODED, not stored with literal =20 in it.
	if parsed.Body != "See the attached file." {
		t.Errorf("body = %q, want %q", parsed.Body, "See the attached file.")
	}

	if len(parsed.Attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(parsed.Attachments))
	}
	att := parsed.Attachments[0]
	if att.Filename != "report.pdf" {
		t.Errorf("filename = %q, want report.pdf", att.Filename)
	}
	if att.ContentType != "application/pdf" {
		t.Errorf("content type = %q, want application/pdf", att.ContentType)
	}
	// Decoded, so it starts with the real PDF magic rather than base64 text.
	if !bytes.HasPrefix(att.Data, []byte("%PDF-1.4")) {
		t.Errorf("attachment data = %q, want it to start with %%PDF-1.4", att.Data)
	}
}

// A text/plain part carrying a filename is a FILE, not the message body.
// Treating it as the body — which the media-type-only check did — silently
// replaced the message the sender wrote and lost the attachment entirely.
func TestParseMultipartTextAttachmentIsNotBody(t *testing.T) {
	body := strings.Join([]string{
		"--B",
		"Content-Type: text/plain",
		"",
		"The real message.",
		"--B",
		"Content-Type: text/plain; name=\"notes.txt\"",
		"Content-Disposition: attachment; filename=\"notes.txt\"",
		"",
		"file contents",
		"--B--",
		"",
	}, "\r\n")

	parsed := &ParsedMessage{}
	if err := newTestProcessor().parseMultipart(bytes.NewReader([]byte(body)), "B", parsed); err != nil {
		t.Fatalf("parseMultipart returned error: %v", err)
	}

	if parsed.Body != "The real message." {
		t.Errorf("body = %q, want the non-attachment part", parsed.Body)
	}
	if len(parsed.Attachments) != 1 || parsed.Attachments[0].Filename != "notes.txt" {
		t.Fatalf("expected notes.txt to be captured as an attachment, got %+v", parsed.Attachments)
	}
	if string(parsed.Attachments[0].Data) != "file contents" {
		t.Errorf("attachment data = %q", parsed.Attachments[0].Data)
	}
}

// Inline images (multipart/related) are attachments too — they are what `cid:`
// references in an HTML body resolve to.
func TestParseMultipartInlineImage(t *testing.T) {
	body := strings.Join([]string{
		"--R",
		"Content-Type: text/html",
		"",
		"<img src=\"cid:logo\">",
		"--R",
		"Content-Type: image/png",
		"Content-Disposition: inline",
		"Content-ID: <logo>",
		"Content-Transfer-Encoding: base64",
		"",
		"iVBORw0KGgo=",
		"--R--",
		"",
	}, "\r\n")

	parsed := &ParsedMessage{}
	if err := newTestProcessor().parseMultipart(bytes.NewReader([]byte(body)), "R", parsed); err != nil {
		t.Fatalf("parseMultipart returned error: %v", err)
	}

	if len(parsed.Attachments) != 1 {
		t.Fatalf("got %d attachments, want 1", len(parsed.Attachments))
	}
	att := parsed.Attachments[0]
	if !att.Inline {
		t.Error("expected the part to be marked inline")
	}
	if att.ContentID != "logo" {
		t.Errorf("content id = %q, want logo (angle brackets stripped)", att.ContentID)
	}
	// No filename was supplied, so one must be synthesised — otherwise the part
	// is undownloadable.
	if att.Filename == "" {
		t.Error("expected a synthesised filename for the unnamed part")
	}
	if !bytes.HasPrefix(att.Data, []byte{0x89, 'P', 'N', 'G'}) {
		t.Errorf("expected decoded PNG magic, got %v", att.Data)
	}
}

// Attachments nested inside multipart/alternative within multipart/mixed are
// the normal shape of mail from Gmail and Outlook.
func TestParseMultipartNestedAttachment(t *testing.T) {
	body := strings.Join([]string{
		"--OUTER",
		"Content-Type: multipart/alternative; boundary=\"INNER\"",
		"",
		"--INNER",
		"Content-Type: text/plain",
		"",
		"plain version",
		"--INNER",
		"Content-Type: text/html",
		"",
		"<p>html version</p>",
		"--INNER--",
		"--OUTER",
		"Content-Type: application/zip",
		"Content-Disposition: attachment; filename=\"bundle.zip\"",
		"",
		"zipbytes",
		"--OUTER--",
		"",
	}, "\r\n")

	parsed := &ParsedMessage{}
	if err := newTestProcessor().parseMultipart(bytes.NewReader([]byte(body)), "OUTER", parsed); err != nil {
		t.Fatalf("parseMultipart returned error: %v", err)
	}

	if parsed.Body != "plain version" {
		t.Errorf("body = %q", parsed.Body)
	}
	if parsed.HTMLBody != "<p>html version</p>" {
		t.Errorf("html body = %q", parsed.HTMLBody)
	}
	if len(parsed.Attachments) != 1 || parsed.Attachments[0].Filename != "bundle.zip" {
		t.Fatalf("expected bundle.zip from the outer part, got %+v", parsed.Attachments)
	}
}

// Filename may be carried only on Content-Type `name=`, with no disposition.
func TestParseMultipartNameFallback(t *testing.T) {
	body := strings.Join([]string{
		"--B",
		"Content-Type: text/plain",
		"",
		"body",
		"--B",
		"Content-Type: application/octet-stream; name=\"data.bin\"",
		"",
		"rawbytes",
		"--B--",
		"",
	}, "\r\n")

	parsed := &ParsedMessage{}
	if err := newTestProcessor().parseMultipart(bytes.NewReader([]byte(body)), "B", parsed); err != nil {
		t.Fatalf("parseMultipart returned error: %v", err)
	}
	if len(parsed.Attachments) != 1 || parsed.Attachments[0].Filename != "data.bin" {
		t.Fatalf("expected data.bin via Content-Type name=, got %+v", parsed.Attachments)
	}
}
