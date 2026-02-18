// Package transport provides email transport mechanisms.
package transport

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/emersion/go-msgauth/dkim"
)

// DKIMConfig contains DKIM signing configuration
type DKIMConfig struct {
	// Domain is the signing domain (e.g., "coldforge.xyz")
	Domain string

	// Selector is the DKIM selector (e.g., "mail" for mail._domainkey.coldforge.xyz)
	Selector string

	// PrivateKey is the PEM-encoded RSA private key
	PrivateKey string

	// HeadersToSign specifies which headers to include in the signature
	// If empty, defaults to: From, To, Subject, Date, Message-ID, MIME-Version, Content-Type
	HeadersToSign []string
}

// DKIMSigner handles DKIM signing of outbound emails
type DKIMSigner struct {
	config     *DKIMConfig
	privateKey *rsa.PrivateKey
	options    *dkim.SignOptions
}

// NewDKIMSigner creates a new DKIM signer from the given configuration
func NewDKIMSigner(config *DKIMConfig) (*DKIMSigner, error) {
	if config == nil {
		return nil, fmt.Errorf("DKIM config is required")
	}

	if config.Domain == "" {
		return nil, fmt.Errorf("DKIM domain is required")
	}

	if config.Selector == "" {
		return nil, fmt.Errorf("DKIM selector is required")
	}

	if config.PrivateKey == "" {
		return nil, fmt.Errorf("DKIM private key is required")
	}

	// Parse the private key
	privateKey, err := parseRSAPrivateKey(config.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DKIM private key: %w", err)
	}

	// Default headers to sign
	headersToSign := config.HeadersToSign
	if len(headersToSign) == 0 {
		headersToSign = []string{
			"From",
			"To",
			"Subject",
			"Date",
			"Message-ID",
			"MIME-Version",
			"Content-Type",
			"Cc",
			"Reply-To",
			"In-Reply-To",
			"References",
		}
	}

	options := &dkim.SignOptions{
		Domain:                 config.Domain,
		Selector:               config.Selector,
		Signer:                 privateKey,
		Hash:                   crypto.SHA256,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed,
		BodyCanonicalization:   dkim.CanonicalizationRelaxed,
		HeaderKeys:             headersToSign,
	}

	return &DKIMSigner{
		config:     config,
		privateKey: privateKey,
		options:    options,
	}, nil
}

// Sign signs an email message and returns the signed message with DKIM-Signature header
func (s *DKIMSigner) Sign(message []byte) ([]byte, error) {
	var signedBuf bytes.Buffer

	// Create a DKIM signer that writes to our buffer
	signer, err := dkim.NewSigner(s.options)
	if err != nil {
		return nil, fmt.Errorf("failed to create DKIM signer: %w", err)
	}

	// Write the message through the signer
	if _, err := signer.Write(message); err != nil {
		return nil, fmt.Errorf("failed to write message to DKIM signer: %w", err)
	}

	// Close to finalize the signature
	if err := signer.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalize DKIM signature: %w", err)
	}

	// Get the DKIM-Signature header
	dkimHeader := fmt.Sprintf("DKIM-Signature: %s\r\n", signer.Signature())

	// Prepend the DKIM-Signature header to the message
	signedBuf.WriteString(dkimHeader)
	signedBuf.Write(message)

	return signedBuf.Bytes(), nil
}

// Domain returns the signing domain
func (s *DKIMSigner) Domain() string {
	return s.config.Domain
}

// Selector returns the DKIM selector
func (s *DKIMSigner) Selector() string {
	return s.config.Selector
}

// parseRSAPrivateKey parses a PEM-encoded RSA private key
func parseRSAPrivateKey(pemData string) (*rsa.PrivateKey, error) {
	// Try to decode as PEM
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		// Maybe it's raw base64 or the key without PEM headers
		// Try wrapping it
		wrapped := fmt.Sprintf("-----BEGIN RSA PRIVATE KEY-----\n%s\n-----END RSA PRIVATE KEY-----", strings.TrimSpace(pemData))
		block, _ = pem.Decode([]byte(wrapped))
		if block == nil {
			return nil, fmt.Errorf("failed to decode PEM block")
		}
	}

	// Try PKCS#1 format first
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err == nil {
		return key, nil
	}

	// Try PKCS#8 format
	keyInterface, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key (tried PKCS#1 and PKCS#8): %w", err)
	}

	rsaKey, ok := keyInterface.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA")
	}

	return rsaKey, nil
}

// GenerateDKIMDNSRecord generates the DNS TXT record value for the public key
func (s *DKIMSigner) GenerateDKIMDNSRecord() string {
	// Extract the public key in PKIX format
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&s.privateKey.PublicKey)
	if err != nil {
		return ""
	}

	// Base64 encode
	pubKeyBase64 := strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})))

	// Remove PEM headers and newlines
	pubKeyBase64 = strings.ReplaceAll(pubKeyBase64, "-----BEGIN PUBLIC KEY-----", "")
	pubKeyBase64 = strings.ReplaceAll(pubKeyBase64, "-----END PUBLIC KEY-----", "")
	pubKeyBase64 = strings.ReplaceAll(pubKeyBase64, "\n", "")
	pubKeyBase64 = strings.TrimSpace(pubKeyBase64)

	return fmt.Sprintf("v=DKIM1; k=rsa; p=%s", pubKeyBase64)
}

// DNSRecordName returns the DNS record name for the DKIM public key
func (s *DKIMSigner) DNSRecordName() string {
	return fmt.Sprintf("%s._domainkey.%s", s.config.Selector, s.config.Domain)
}
