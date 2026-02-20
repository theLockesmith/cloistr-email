package unit

import (
	"context"
	"testing"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewDKIMVerifier(t *testing.T) {
	logger := zap.NewNop()

	t.Run("with default options", func(t *testing.T) {
		verifier := transport.NewDKIMVerifier(logger)
		require.NotNil(t, verifier)
	})

	t.Run("with custom timeout", func(t *testing.T) {
		verifier := transport.NewDKIMVerifier(logger,
			transport.WithDKIMVerifyTimeout(60*time.Second),
		)
		require.NotNil(t, verifier)
	})
}

func TestDKIMVerifyNoSignature(t *testing.T) {
	logger := zap.NewNop()
	verifier := transport.NewDKIMVerifier(logger)

	// Message without DKIM signature
	message := []byte(`From: sender@example.com
To: recipient@example.com
Subject: Test
Date: Mon, 17 Feb 2026 10:00:00 +0000
Message-ID: <test@example.com>

Hello, this is a test.
`)

	result := verifier.Verify(context.Background(), message)

	assert.False(t, result.Valid)
	assert.Contains(t, result.Error, "no DKIM signatures found")
	assert.Empty(t, result.Signatures)
}

func TestDKIMVerificationResultStruct(t *testing.T) {
	result := &transport.DKIMVerificationResult{
		Valid: true,
		Signatures: []transport.DKIMSignatureResult{
			{
				Domain:          "example.com",
				Selector:        "mail",
				Valid:           true,
				HeadersIncluded: []string{"from", "to", "subject"},
			},
		},
	}

	assert.True(t, result.Valid)
	assert.Len(t, result.Signatures, 1)
	assert.Equal(t, "example.com", result.Signatures[0].Domain)
	assert.True(t, result.Signatures[0].Valid)
}

func TestDKIMSignatureResultStruct(t *testing.T) {
	t.Run("valid signature", func(t *testing.T) {
		sig := &transport.DKIMSignatureResult{
			Domain:          "coldforge.xyz",
			Selector:        "mail",
			Valid:           true,
			HeadersIncluded: []string{"from", "to", "subject", "date"},
		}

		assert.Equal(t, "coldforge.xyz", sig.Domain)
		assert.Equal(t, "mail", sig.Selector)
		assert.True(t, sig.Valid)
		assert.Empty(t, sig.Error)
	})

	t.Run("invalid signature", func(t *testing.T) {
		sig := &transport.DKIMSignatureResult{
			Domain:   "example.com",
			Selector: "mail",
			Valid:    false,
			Error:    "public key not found",
		}

		assert.False(t, sig.Valid)
		assert.NotEmpty(t, sig.Error)
	})
}

func TestDKIMVerifyConfig(t *testing.T) {
	config := &transport.DKIMVerifyConfig{
		RequireValid:    true,
		RequiredDomains: []string{"coldforge.xyz", "example.com"},
	}

	assert.True(t, config.RequireValid)
	assert.Len(t, config.RequiredDomains, 2)
}

func TestGetDKIMResultFromContext(t *testing.T) {
	t.Run("no result in context", func(t *testing.T) {
		ctx := context.Background()
		result := transport.GetDKIMResult(ctx)
		assert.Nil(t, result)
	})
}

// Note: Full DKIM verification with actual signatures requires
// DNS lookups for public keys and is tested in integration tests.
