package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func newTestTransport(t *testing.T) *SMTPTransport {
	t.Helper()
	tr, err := NewSMTPTransport(&SMTPConfig{Host: "localhost", Port: 25}, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewSMTPTransport: %v", err)
	}
	return tr
}

// decodeB64Part reads a base64-encoded MIME part body (mime/multipart does not
// auto-decode base64) and returns the raw bytes.
func decodeB64Part(t *testing.T, p *multipart.Part) []byte {
	t.Helper()
	raw, _ := io.ReadAll(p)
	clean := strings.NewReplacer("\r", "", "\n", "").Replace(string(raw))
	data, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		t.Fatalf("base64 decode part: %v", err)
	}
	return data
}

func TestBuildRawEmailWithAttachment(t *testing.T) {
	tr := newTestTransport(t)
	attData := []byte{0x89, 'P', 'N', 'G', 0x00, 0xff, 0x10, 'x'}

	msg := &Message{
		FromAddress: "alice@cloistr.xyz",
		ToAddresses: []string{"bob@example.com"},
		Subject:     "hi",
		Body:        "hello body",
		Attachments: []Attachment{
			{Filename: "logo.png", ContentType: "image/png", Data: attData},
		},
	}

	raw, err := tr.buildRawEmail(context.Background(), msg)
	if err != nil {
		t.Fatalf("buildRawEmail: %v", err)
	}

	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content-type: %v", err)
	}
	if mediaType != "multipart/mixed" {
		t.Fatalf("top-level media type = %q, want multipart/mixed", mediaType)
	}

	mr := multipart.NewReader(m.Body, params["boundary"])
	var sawBody, sawAttachment bool
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		disp := p.Header.Get("Content-Disposition")
		switch {
		case strings.HasPrefix(disp, "attachment"):
			sawAttachment = true
			if fn := p.FileName(); fn != "logo.png" {
				t.Errorf("attachment filename = %q, want logo.png", fn)
			}
			if ct := p.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
				t.Errorf("attachment content-type = %q", ct)
			}
			got := decodeB64Part(t, p)
			if !bytes.Equal(got, attData) {
				t.Errorf("attachment bytes mismatch: got %v want %v", got, attData)
			}
		default:
			sawBody = true
			got := decodeB64Part(t, p)
			if string(got) != "hello body" {
				t.Errorf("body part = %q, want 'hello body'", got)
			}
		}
	}
	if !sawBody {
		t.Error("no body part found")
	}
	if !sawAttachment {
		t.Error("no attachment part found")
	}
}

func TestBuildRawEmailNoAttachmentStaysSinglePart(t *testing.T) {
	tr := newTestTransport(t)
	msg := &Message{
		FromAddress: "alice@cloistr.xyz",
		ToAddresses: []string{"bob@example.com"},
		Subject:     "hi",
		Body:        "just text",
	}
	raw, err := tr.buildRawEmail(context.Background(), msg)
	if err != nil {
		t.Fatalf("buildRawEmail: %v", err)
	}
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	mediaType, _, _ := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if mediaType != "text/plain" {
		t.Errorf("no-attachment media type = %q, want text/plain", mediaType)
	}
}
