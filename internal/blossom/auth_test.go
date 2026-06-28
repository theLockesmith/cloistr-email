package blossom

import (
	"context"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func TestEventAuthSignerProducesValidSignedAuth(t *testing.T) {
	pub, err := nostr.GetPublicKey(testPrivKey)
	if err != nil {
		t.Fatalf("pubkey: %v", err)
	}

	// Bridge an arbitrary event-signing func (here a local key) into an
	// AuthSigner — this is how the email service plugs in NIP-46 SignEvent.
	signer := NewEventAuthSigner(pub, func(ctx context.Context, ev *nostr.Event) error {
		return ev.Sign(testPrivKey)
	})

	if signer.PublicKey() != pub {
		t.Errorf("PublicKey = %q, want %q", signer.PublicKey(), pub)
	}

	exp := time.Now().Add(5 * time.Minute)
	ev, err := signer.SignAuth(context.Background(), "upload", []string{"abc123"}, exp)
	if err != nil {
		t.Fatalf("SignAuth: %v", err)
	}

	if ev.Kind != authEventKind {
		t.Errorf("kind = %d, want %d", ev.Kind, authEventKind)
	}
	if ev.PubKey != pub {
		t.Errorf("event pubkey = %q, want %q", ev.PubKey, pub)
	}
	if ok, _ := ev.CheckSignature(); !ok {
		t.Error("signature does not verify")
	}
	// Verb + hash + expiration tags present.
	var gotVerb, gotHash, gotExp string
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "t":
			gotVerb = tag[1]
		case "x":
			gotHash = tag[1]
		case "expiration":
			gotExp = tag[1]
		}
	}
	if gotVerb != "upload" {
		t.Errorf("verb tag = %q, want upload", gotVerb)
	}
	if gotHash != "abc123" {
		t.Errorf("hash tag = %q, want abc123", gotHash)
	}
	if gotExp == "" {
		t.Error("missing expiration tag")
	}
}

func TestEventAuthSignerPropagatesSignError(t *testing.T) {
	signer := NewEventAuthSigner("deadbeef", func(ctx context.Context, ev *nostr.Event) error {
		return context.Canceled
	})
	if _, err := signer.SignAuth(context.Background(), "delete", []string{"h"}, time.Now()); err == nil {
		t.Error("expected error to propagate from sign func")
	}
}
