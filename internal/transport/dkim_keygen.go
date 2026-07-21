package transport

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// DefaultDKIMKeyBits is the RSA key size used for generated DKIM keys. 2048 is
// the interoperable sweet spot: strong enough, and its public key still fits a
// single DNS TXT string (4096-bit keys must be split across strings).
const DefaultDKIMKeyBits = 2048

// GeneratedDKIMKey is a freshly generated DKIM keypair ready to persist and
// publish. PrivatePEM is the secret (store it, never expose it); DNSRecordName
// and DNSRecordValue are what the operator publishes.
type GeneratedDKIMKey struct {
	Domain         string
	Selector       string
	PrivatePEM     string // PKCS#1 PEM — parseRSAPrivateKey accepts this
	DNSRecordName  string // <selector>._domainkey.<domain>
	DNSRecordValue string // v=DKIM1; k=rsa; p=<base64 pubkey>
}

// GenerateDKIMKey creates a new RSA DKIM keypair for domain/selector and
// returns the private key plus the public DNS record to publish. bits<=0 uses
// DefaultDKIMKeyBits.
func GenerateDKIMKey(domain, selector string, bits int) (*GeneratedDKIMKey, error) {
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if selector == "" {
		selector = "mail"
	}
	if bits <= 0 {
		bits = DefaultDKIMKeyBits
	}

	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	// Reuse the signer to derive the DNS record so the published public key is
	// always exactly the one that will sign.
	signer, err := NewDKIMSigner(&DKIMConfig{
		Domain:     domain,
		Selector:   selector,
		PrivateKey: string(privatePEM),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build signer for generated key: %w", err)
	}

	return &GeneratedDKIMKey{
		Domain:         domain,
		Selector:       selector,
		PrivatePEM:     string(privatePEM),
		DNSRecordName:  signer.DNSRecordName(),
		DNSRecordValue: signer.GenerateDKIMDNSRecord(),
	}, nil
}
