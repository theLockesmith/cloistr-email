package unit

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestRSAKey generates a test RSA private key
func generateTestRSAKey(t *testing.T) string {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	pemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	}

	return string(pem.EncodeToMemory(pemBlock))
}

func TestNewDKIMSigner(t *testing.T) {
	privateKey := generateTestRSAKey(t)

	tests := []struct {
		name        string
		config      *transport.DKIMConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			config: &transport.DKIMConfig{
				Domain:     "example.com",
				Selector:   "mail",
				PrivateKey: privateKey,
			},
			expectError: false,
		},
		{
			name:        "nil config",
			config:      nil,
			expectError: true,
			errorMsg:    "config is required",
		},
		{
			name: "missing domain",
			config: &transport.DKIMConfig{
				Selector:   "mail",
				PrivateKey: privateKey,
			},
			expectError: true,
			errorMsg:    "domain is required",
		},
		{
			name: "missing selector",
			config: &transport.DKIMConfig{
				Domain:     "example.com",
				PrivateKey: privateKey,
			},
			expectError: true,
			errorMsg:    "selector is required",
		},
		{
			name: "missing private key",
			config: &transport.DKIMConfig{
				Domain:   "example.com",
				Selector: "mail",
			},
			expectError: true,
			errorMsg:    "private key is required",
		},
		{
			name: "invalid private key",
			config: &transport.DKIMConfig{
				Domain:     "example.com",
				Selector:   "mail",
				PrivateKey: "not-a-valid-key",
			},
			expectError: true,
			errorMsg:    "failed to parse",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signer, err := transport.NewDKIMSigner(tt.config)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				assert.Nil(t, signer)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, signer)
				assert.Equal(t, tt.config.Domain, signer.Domain())
				assert.Equal(t, tt.config.Selector, signer.Selector())
			}
		})
	}
}

func TestDKIMSignerSign(t *testing.T) {
	privateKey := generateTestRSAKey(t)

	config := &transport.DKIMConfig{
		Domain:     "coldforge.xyz",
		Selector:   "mail",
		PrivateKey: privateKey,
	}

	signer, err := transport.NewDKIMSigner(config)
	require.NoError(t, err)

	// Create a simple email message
	message := []byte("From: sender@coldforge.xyz\r\n" +
		"To: recipient@example.com\r\n" +
		"Subject: Test Email\r\n" +
		"Date: Mon, 17 Feb 2026 10:00:00 +0000\r\n" +
		"Message-ID: <test@coldforge.xyz>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Hello, this is a test email.\r\n")

	signed, err := signer.Sign(message)
	require.NoError(t, err)

	// Verify the signed message has a DKIM-Signature header
	signedStr := string(signed)
	assert.True(t, strings.HasPrefix(signedStr, "DKIM-Signature:"),
		"Signed message should start with DKIM-Signature header")

	// Verify the signature contains expected fields
	assert.Contains(t, signedStr, "d=coldforge.xyz", "Signature should contain domain")
	assert.Contains(t, signedStr, "s=mail", "Signature should contain selector")
	assert.Contains(t, signedStr, "a=rsa-sha256", "Signature should contain algorithm")

	// Verify the original message is preserved after the signature
	assert.Contains(t, signedStr, "From: sender@coldforge.xyz", "Original From header should be present")
	assert.Contains(t, signedStr, "Hello, this is a test email.", "Original body should be present")
}

func TestDKIMSignerDNSRecord(t *testing.T) {
	privateKey := generateTestRSAKey(t)

	config := &transport.DKIMConfig{
		Domain:     "coldforge.xyz",
		Selector:   "mail",
		PrivateKey: privateKey,
	}

	signer, err := transport.NewDKIMSigner(config)
	require.NoError(t, err)

	// Test DNS record name
	dnsName := signer.DNSRecordName()
	assert.Equal(t, "mail._domainkey.coldforge.xyz", dnsName)

	// Test DNS record value
	dnsRecord := signer.GenerateDKIMDNSRecord()
	assert.True(t, strings.HasPrefix(dnsRecord, "v=DKIM1;"),
		"DNS record should start with v=DKIM1")
	assert.Contains(t, dnsRecord, "k=rsa", "DNS record should specify RSA key type")
	assert.Contains(t, dnsRecord, "p=", "DNS record should contain public key")
}

func TestDKIMSignerCustomHeaders(t *testing.T) {
	privateKey := generateTestRSAKey(t)

	config := &transport.DKIMConfig{
		Domain:     "coldforge.xyz",
		Selector:   "mail",
		PrivateKey: privateKey,
		HeadersToSign: []string{
			"From",
			"To",
			"Subject",
		},
	}

	signer, err := transport.NewDKIMSigner(config)
	require.NoError(t, err)
	require.NotNil(t, signer)

	// Sign a message
	message := []byte("From: sender@coldforge.xyz\r\n" +
		"To: recipient@example.com\r\n" +
		"Subject: Test\r\n" +
		"\r\n" +
		"Body\r\n")

	signed, err := signer.Sign(message)
	require.NoError(t, err)
	assert.NotEmpty(t, signed)
}
