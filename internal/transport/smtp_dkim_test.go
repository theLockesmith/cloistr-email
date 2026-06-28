package transport

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"go.uber.org/zap"
)

func genDKIMSigner(t *testing.T, domain string) *DKIMSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	s, err := NewDKIMSigner(&DKIMConfig{Domain: domain, Selector: "mail", PrivateKey: string(pemBytes)})
	if err != nil {
		t.Fatalf("NewDKIMSigner(%s): %v", domain, err)
	}
	return s
}

func TestDKIMSignerForSelectsByFromDomain(t *testing.T) {
	tr, err := NewSMTPTransport(&SMTPConfig{Host: "localhost", Port: 25}, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("transport: %v", err)
	}

	cloistr := genDKIMSigner(t, "cloistr.xyz")
	aegis := genDKIMSigner(t, "aegis-hq.xyz")
	signers := map[string]*DKIMSigner{
		"cloistr.xyz":  cloistr,
		"aegis-hq.xyz": aegis,
	}
	tr.WithDKIMProvider(DKIMProviderFunc(func(domain string) *DKIMSigner {
		return signers[domain]
	}))

	cases := []struct {
		from string
		want *DKIMSigner
	}{
		{"alice@cloistr.xyz", cloistr},
		{"bob@aegis-hq.xyz", aegis},
		{"Carol@CLOISTR.XYZ", cloistr}, // case-insensitive domain
		{"dave@unknown.example", nil},  // no signer, no legacy fallback
		{"no-at-sign", nil},
	}
	for _, tc := range cases {
		got := tr.dkimSignerFor(tc.from)
		if got != tc.want {
			t.Errorf("dkimSignerFor(%q) = %v, want %v", tc.from, got, tc.want)
		}
	}
}

func TestDKIMSignerForLegacySingleSigner(t *testing.T) {
	// With no provider, the single configured signer is used for any domain
	// (preserves pre-multi-domain behavior).
	legacy := genDKIMSigner(t, "coldforge.xyz")
	tr, err := NewSMTPTransport(&SMTPConfig{Host: "localhost", Port: 25}, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	tr.dkimSigner = legacy

	if got := tr.dkimSignerFor("anyone@whatever.com"); got != legacy {
		t.Errorf("legacy fallback failed: got %v want %v", got, legacy)
	}

	// Once a provider exists, the legacy signer is only used when its domain
	// matches the From domain (so we never sign with a mismatched d=).
	tr.WithDKIMProvider(DKIMProviderFunc(func(string) *DKIMSigner { return nil }))
	if got := tr.dkimSignerFor("a@whatever.com"); got != nil {
		t.Errorf("expected nil for mismatched legacy domain, got %v", got)
	}
	if got := tr.dkimSignerFor("a@coldforge.xyz"); got != legacy {
		t.Errorf("expected legacy signer for matching domain, got %v", got)
	}
}
