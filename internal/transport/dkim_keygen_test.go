package transport

import (
	"strings"
	"testing"
)

func TestGenerateDKIMKey(t *testing.T) {
	key, err := GenerateDKIMKey("cloistr.xyz", "mail", 2048)
	if err != nil {
		t.Fatalf("GenerateDKIMKey: %v", err)
	}

	if key.DNSRecordName != "mail._domainkey.cloistr.xyz" {
		t.Errorf("DNSRecordName = %q", key.DNSRecordName)
	}
	if !strings.HasPrefix(key.DNSRecordValue, "v=DKIM1; k=rsa; p=") {
		t.Errorf("DNSRecordValue = %q", key.DNSRecordValue)
	}
	if !strings.Contains(key.PrivatePEM, "PRIVATE KEY") {
		t.Errorf("PrivatePEM does not look like PEM")
	}

	// The generated private key must be usable by the signer, and the signer's
	// derived DNS record must match the one returned by keygen (same key).
	signer, err := NewDKIMSigner(&DKIMConfig{
		Domain: "cloistr.xyz", Selector: "mail", PrivateKey: key.PrivatePEM,
	})
	if err != nil {
		t.Fatalf("generated key is not loadable by signer: %v", err)
	}
	if got := signer.GenerateDKIMDNSRecord(); got != key.DNSRecordValue {
		t.Errorf("signer record %q != keygen record %q", got, key.DNSRecordValue)
	}
}

func TestGenerateDKIMKeyDefaults(t *testing.T) {
	key, err := GenerateDKIMKey("example.com", "", 0)
	if err != nil {
		t.Fatalf("GenerateDKIMKey: %v", err)
	}
	if key.Selector != "mail" {
		t.Errorf("default selector = %q, want mail", key.Selector)
	}
}

func TestGenerateDKIMKeyRequiresDomain(t *testing.T) {
	if _, err := GenerateDKIMKey("", "mail", 2048); err == nil {
		t.Fatal("expected error for empty domain")
	}
}
