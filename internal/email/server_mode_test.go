package email

import (
	"errors"
	"strings"
	"testing"
)

// Server-side mode encrypts one body to one recipient's key. Anything that
// body cannot cover used to go out in plaintext (no key, attachments) or
// undecryptable (extra recipients) while the user believed it was encrypted.
// The send is now refused up front with a reason the UI can show.
func TestCheckServerModeSendable(t *testing.T) {
	bobKey := strings.Repeat("b", 64)
	base := func() (*SendRequest, map[string]string) {
		return &SendRequest{To: []string{"bob@cloistr.xyz"}, Body: "hi"},
			map[string]string{"bob@cloistr.xyz": bobKey}
	}

	req, keys := base()
	if err := checkServerModeSendable(req, keys); err != nil {
		t.Fatalf("one recipient with a key, no attachments: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*SendRequest, map[string]string)
		want   error
	}{
		{"second To", func(r *SendRequest, _ map[string]string) { r.To = append(r.To, "carol@cloistr.xyz") }, ErrServerModeOneRecipient},
		{"CC", func(r *SendRequest, _ map[string]string) { r.CC = []string{"carol@cloistr.xyz"} }, ErrServerModeOneRecipient},
		{"BCC", func(r *SendRequest, _ map[string]string) { r.BCC = []string{"carol@cloistr.xyz"} }, ErrServerModeOneRecipient},
		{"no recipient", func(r *SendRequest, _ map[string]string) { r.To = nil }, ErrServerModeOneRecipient},
		{"recipient without key", func(_ *SendRequest, k map[string]string) { delete(k, "bob@cloistr.xyz") }, ErrServerRecipientNoKey},
		{"attachment", func(r *SendRequest, _ map[string]string) {
			r.Attachments = []AttachmentInput{{Filename: "a.txt", ContentType: "text/plain", Data: []byte("x")}}
		}, ErrServerModeAttachments},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, keys := base()
			c.mutate(req, keys)
			if err := checkServerModeSendable(req, keys); !errors.Is(err, c.want) {
				t.Fatalf("err = %v; want %v", err, c.want)
			}
		})
	}
}
