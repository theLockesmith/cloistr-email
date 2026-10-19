package transport

import (
	"context"
	"testing"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"go.uber.org/zap"
)

// The delivery result said Encrypted:true for every recipient of a
// pre-encrypted message, so a CC with no wrapped key was reported encrypted.
// It must be true only for recipients who can actually decrypt.
func TestDeliveredEncrypted_PreEncrypted(t *testing.T) {
	msg := &Message{
		IsPreEncrypted:      true,
		ToAddresses:         []string{"bob@x"},
		CCAddresses:         []string{"carol@x"},
		RecipientPubkeys:    map[string]string{"bob@x": "b", "carol@x": "c"},
		EncryptedRecipients: map[string]bool{"bob@x": true},
	}
	if !deliveredEncrypted(msg, "", "bob@x") {
		t.Error("To with a wrapped key must report encrypted")
	}
	if deliveredEncrypted(msg, "", "carol@x") {
		t.Error("CC with no wrapped key reported encrypted")
	}
}

type recordingEncryptor struct{}

func (recordingEncryptor) EncryptContent(_ context.Context, _, recipient, pt string) (string, error) {
	return "enc-for-" + recipient, nil
}
func (recordingEncryptor) DecryptContent(context.Context, string, string, string) (string, error) {
	return "", nil
}

// Server-side mode encrypts the body to ONE recipient (the first To with a
// key), yet every recipient with a known pubkey was reported encrypted.
func TestDeliveredEncrypted_ServerSide(t *testing.T) {
	tr := newTestTransport(t)
	tr.encryptor = encryption.NewEmailEncryptor(recordingEncryptor{}, nil, zap.NewNop())
	msg := &Message{
		FromAddress:         "alice@cloistr.xyz",
		ToAddresses:         []string{"bob@x"},
		CCAddresses:         []string{"carol@x"},
		SenderPubkey:        "a",
		RecipientPubkeys:    map[string]string{"bob@x": "b", "carol@x": "c"},
		EncryptionRequested: true,
		Subject:             "s",
		Body:                "hello",
	}
	_, encryptedTo, err := tr.buildRawEmailReport(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if encryptedTo != "bob@x" {
		t.Fatalf("encryptedTo = %q, want bob@x", encryptedTo)
	}
	if !deliveredEncrypted(msg, encryptedTo, "bob@x") || deliveredEncrypted(msg, encryptedTo, "carol@x") {
		t.Error("only the recipient the body was encrypted to may be reported encrypted")
	}
}
