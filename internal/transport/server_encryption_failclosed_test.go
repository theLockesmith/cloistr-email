package transport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"go.uber.org/zap"
)

// Server-side mode used to fall back to plaintext: if the recipient had no key,
// or encrypting failed (for example the user's signer connection was down), the
// body went out in the clear with only a log line. The user had asked for
// encryption. Every such case must now refuse to build the message.

type fakeContentEncryptor struct{ err error }

func (f fakeContentEncryptor) EncryptContent(_ context.Context, _, _, plaintext string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "CIPHERTEXT(" + strings.Repeat("x", len(plaintext)) + ")", nil
}

func (f fakeContentEncryptor) DecryptContent(context.Context, string, string, string) (string, error) {
	return "", errors.New("unused")
}

func transportWithEncryptor(t *testing.T, enc encryption.Encryptor) *SMTPTransport {
	t.Helper()
	var ee *encryption.EmailEncryptor
	if enc != nil {
		ee = encryption.NewEmailEncryptor(enc, nil, zap.NewNop())
	}
	tr, err := NewSMTPTransport(&SMTPConfig{Host: "localhost", Port: 25}, ee, zap.NewNop())
	if err != nil {
		t.Fatalf("NewSMTPTransport: %v", err)
	}
	return tr
}

const secretBody = "TOP-SECRET-PLAINTEXT-BODY"

func serverMsg() *Message {
	return &Message{
		FromAddress:         "alice@cloistr.xyz",
		ToAddresses:         []string{"bob@cloistr.xyz"},
		SenderPubkey:        strings.Repeat("a", 64),
		RecipientPubkeys:    map[string]string{"bob@cloistr.xyz": strings.Repeat("b", 64)},
		Subject:             "s",
		Body:                secretBody,
		HTMLBody:            "<p>" + secretBody + "</p>",
		EncryptionRequested: true,
	}
}

func TestServerEncryption_FailsClosed(t *testing.T) {
	cases := map[string]struct {
		enc    encryption.Encryptor
		mutate func(*Message)
	}{
		"encrypt call fails":      {enc: fakeContentEncryptor{err: errors.New("signer not connected")}},
		"no encryptor configured": {enc: nil},
		"recipient has no key": {
			enc:    fakeContentEncryptor{},
			mutate: func(m *Message) { m.RecipientPubkeys = map[string]string{} },
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			msg := serverMsg()
			if c.mutate != nil {
				c.mutate(msg)
			}
			raw, _, err := transportWithEncryptor(t, c.enc).buildRawEmailReport(context.Background(), msg)
			if err == nil {
				t.Fatalf("built a message in server-side mode without encrypting it:\n%s", raw)
			}
			if !errors.Is(err, ErrServerEncryption) {
				t.Errorf("error %v is not ErrServerEncryption", err)
			}
			if strings.Contains(string(raw), secretBody) {
				t.Fatal("plaintext body present in the output")
			}
		})
	}
}

func TestServerEncryption_EncryptsForTheOneRecipient(t *testing.T) {
	raw, to, err := transportWithEncryptor(t, fakeContentEncryptor{}).buildRawEmailReport(context.Background(), serverMsg())
	if err != nil {
		t.Fatal(err)
	}
	if to != "bob@cloistr.xyz" {
		t.Errorf("encrypted to %q; want bob@cloistr.xyz", to)
	}
	if strings.Contains(string(raw), secretBody) {
		t.Fatalf("plaintext body (or HTML) present in an encrypted message:\n%s", raw)
	}
	if !strings.Contains(string(raw), encryption.HeaderNostrEncrypted+": true") {
		t.Error("missing X-Nostr-Encrypted header")
	}
}

func TestServerEncryption_RefusesWhatOneBodyCannotCover(t *testing.T) {
	cases := map[string]func(*Message){
		"second To recipient": func(m *Message) { m.ToAddresses = append(m.ToAddresses, "carol@cloistr.xyz") },
		"CC recipient":        func(m *Message) { m.CCAddresses = []string{"carol@cloistr.xyz"} },
		"BCC recipient":       func(m *Message) { m.BCCAddresses = []string{"carol@cloistr.xyz"} },
		"attachment": func(m *Message) {
			m.Attachments = []Attachment{{Filename: "a.txt", ContentType: "text/plain", Data: []byte(secretBody)}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			msg := serverMsg()
			mutate(msg)
			raw, _, err := transportWithEncryptor(t, fakeContentEncryptor{}).buildRawEmailReport(context.Background(), msg)
			if !errors.Is(err, ErrServerEncryption) {
				t.Fatalf("err = %v; want ErrServerEncryption (a recipient or file would get plaintext or undecryptable mail)", err)
			}
			if strings.Contains(string(raw), secretBody) {
				t.Fatal("plaintext present in the output")
			}
		})
	}
}
