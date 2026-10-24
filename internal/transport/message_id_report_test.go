package transport

import (
	"context"
	"strings"
	"testing"
)

// The header-injection fix (2026-10-19) made buildRawEmail work on a sanitised
// COPY of the message and generated the Message-ID on that copy, so Send
// returned an empty MessageID. The sender's Sent copy was then stored with
// message_id "" and every Sent copy after the first hit the unique constraint
// and was dropped (28 of 30 production sends, 2026-10-19..20). The caller must
// get back the Message-ID the recipient sees.
func TestBuildRawEmail_ReportsGeneratedMessageID(t *testing.T) {
	tr := newTestTransport(t)
	msg := &Message{
		FromAddress: "alice@cloistr.xyz",
		ToAddresses: []string{"bob@example.com"},
		Subject:     "s",
		Body:        "b",
	}
	raw, err := tr.buildRawEmail(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	if msg.MessageID == "" {
		t.Fatal("buildRawEmail generated a Message-ID but left the caller's message without it")
	}
	hdr, _ := headerBlock(raw)
	if !strings.Contains(hdr, "Message-ID: <"+msg.MessageID+">") {
		t.Fatalf("reported Message-ID %q is not the one in the message:\n%s", msg.MessageID, hdr)
	}
}

func TestBuildRawEmail_KeepsCallerMessageID(t *testing.T) {
	tr := newTestTransport(t)
	msg := &Message{FromAddress: "alice@cloistr.xyz", ToAddresses: []string{"bob@example.com"}, Subject: "s", Body: "b", MessageID: "given@cloistr.xyz"}
	raw, err := tr.buildRawEmail(context.Background(), msg)
	if err != nil {
		t.Fatal(err)
	}
	hdr, _ := headerBlock(raw)
	if msg.MessageID != "given@cloistr.xyz" || !strings.Contains(hdr, "Message-ID: <given@cloistr.xyz>") {
		t.Fatalf("caller's Message-ID not kept: %q\n%s", msg.MessageID, hdr)
	}
}
