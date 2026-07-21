package domains

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/transport"
)

type stubLister struct{ domains []*storage.Domain }

func (s stubLister) ListActiveDomains(_ context.Context) ([]*storage.Domain, error) {
	return s.domains, nil
}

func strptr(s string) *string { return &s }

// The registry must only build a signer for a VERIFIED domain — an
// active-but-unverified domain (e.g. mid-rotation) must NOT sign, so it never
// signs with a selector whose DNS record isn't published.
func TestRegistryReload_OnlySignsVerified(t *testing.T) {
	key, err := transport.GenerateDKIMKey("verified.xyz", "mail", 2048)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	// A valid, loadable key but for a domain we'll leave unverified.
	unverifiedKey, err := transport.GenerateDKIMKey("pending.xyz", "mail", 2048)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	lister := stubLister{domains: []*storage.Domain{
		{Domain: "verified.xyz", DKIMSelector: "mail", DKIMPrivateKey: strptr(key.PrivatePEM), Verified: true, Active: true},
		{Domain: "pending.xyz", DKIMSelector: "mail", DKIMPrivateKey: strptr(unverifiedKey.PrivatePEM), Verified: false, Active: true},
		{Domain: "nokey.xyz", DKIMSelector: "mail", DKIMPrivateKey: nil, Verified: true, Active: true},
	}}

	reg := NewRegistry(lister, nil, zap.NewNop())
	if err := reg.Reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if reg.DKIMSignerFor("verified.xyz") == nil {
		t.Error("verified domain should have a signer")
	}
	if reg.DKIMSignerFor("pending.xyz") != nil {
		t.Error("unverified domain must NOT have a signer (would sign with unpublished selector)")
	}
	if reg.DKIMSignerFor("nokey.xyz") != nil {
		t.Error("domain without a key must not have a signer")
	}
	if reg.DKIMSignerFor("unknown.xyz") != nil {
		t.Error("unknown domain returns nil (transport falls back to legacy signer)")
	}
}

// PublishReload with no Redis client must still refresh the local map.
func TestRegistryPublishReload_NoRedisRefreshesLocal(t *testing.T) {
	key, _ := transport.GenerateDKIMKey("a.xyz", "mail", 2048)
	lister := stubLister{domains: []*storage.Domain{
		{Domain: "a.xyz", DKIMSelector: "mail", DKIMPrivateKey: strptr(key.PrivatePEM), Verified: true, Active: true},
	}}
	reg := NewRegistry(lister, nil, zap.NewNop())

	if reg.DKIMSignerFor("a.xyz") != nil {
		t.Fatal("precondition: no signer before reload")
	}
	if err := reg.PublishReload(context.Background()); err != nil {
		t.Fatalf("PublishReload: %v", err)
	}
	if reg.DKIMSignerFor("a.xyz") == nil {
		t.Error("PublishReload must refresh the local signer map even without Redis")
	}
}
