package unit

import (
	"context"
	"testing"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/email"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/signing"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestLocalSignerCreation tests creating a signer from a private key
func TestLocalSignerCreation(t *testing.T) {
	tests := []struct {
		name        string
		privateKey  string
		expectError bool
	}{
		{
			name:        "valid private key",
			privateKey:  nostr.GeneratePrivateKey(),
			expectError: false,
		},
		{
			name:        "invalid private key",
			privateKey:  "invalid",
			expectError: true,
		},
		{
			name:        "empty private key",
			privateKey:  "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signer, err := signing.NewLocalSigner(tt.privateKey)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, signer)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, signer)
				assert.NotEmpty(t, signer.PublicKey())
				assert.Equal(t, 64, len(signer.PublicKey()), "Public key should be 64 hex chars")
			}
		})
	}
}

// TestLocalSignerSign tests signing messages
func TestLocalSignerSign(t *testing.T) {
	privateKey := nostr.GeneratePrivateKey()
	signer, err := signing.NewLocalSigner(privateKey)
	require.NoError(t, err)

	ctx := context.Background()

	tests := []struct {
		name    string
		message []byte
	}{
		{
			name:    "sign simple message",
			message: []byte("Hello, World!"),
		},
		{
			name:    "sign empty message",
			message: []byte(""),
		},
		{
			name:    "sign long message",
			message: []byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit. " +
				"Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua."),
		},
		{
			name:    "sign message with special chars",
			message: []byte("Test with émojis 🎉 and unicode: 日本語"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig, err := signer.Sign(ctx, tt.message)

			assert.NoError(t, err)
			assert.NotEmpty(t, sig)
			assert.Equal(t, 128, len(sig), "Signature should be 128 hex chars (64 bytes)")
		})
	}
}

// TestEmailSignerSign tests the email signing workflow
func TestEmailSignerSign(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	privateKey := nostr.GeneratePrivateKey()
	signer, err := signing.NewLocalSigner(privateKey)
	require.NoError(t, err)

	emailSigner := email.NewEmailSigner(logger)
	ctx := context.Background()

	tests := []struct {
		name    string
		email   *email.SignableEmail
		wantErr bool
	}{
		{
			name: "sign email with all headers",
			email: &email.SignableEmail{
				Headers: map[string]string{
					"from":       "alice@coldforge.xyz",
					"to":         "bob@example.com",
					"date":       "Mon, 17 Feb 2026 10:00:00 +0000",
					"message-id": "test-123@coldforge.xyz",
					"subject":    "Test Email",
				},
				Body:      "Hello Bob!",
				MessageID: "test-123@coldforge.xyz",
				Date:      "Mon, 17 Feb 2026 10:00:00 +0000",
			},
			wantErr: false,
		},
		{
			name: "sign email with minimal headers",
			email: &email.SignableEmail{
				Headers: map[string]string{
					"from": "alice@coldforge.xyz",
					"to":   "bob@example.com",
				},
				Body: "Minimal email",
			},
			wantErr: false,
		},
		{
			name: "sign email with CC and threading headers",
			email: &email.SignableEmail{
				Headers: map[string]string{
					"from":        "alice@coldforge.xyz",
					"to":          "bob@example.com",
					"cc":          "charlie@example.com",
					"date":        "Mon, 17 Feb 2026 10:00:00 +0000",
					"message-id":  "test-456@coldforge.xyz",
					"subject":     "Re: Previous Email",
					"in-reply-to": "<prev-123@coldforge.xyz>",
					"references":  "<orig-001@coldforge.xyz> <prev-123@coldforge.xyz>",
				},
				Body:      "This is a reply.",
				MessageID: "test-456@coldforge.xyz",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := emailSigner.Sign(ctx, tt.email, signer)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)

			// Verify result fields
			assert.NotEmpty(t, result.Signature)
			assert.Equal(t, 128, len(result.Signature), "Signature should be 128 hex chars")
			assert.Equal(t, signer.PublicKey(), result.Pubkey)
			assert.NotEmpty(t, result.SignedHeaders)
			assert.NotEmpty(t, result.CanonicalData)
		})
	}
}

// TestSignatureHeaders tests that signature headers are correctly formatted
func TestSignatureHeaders(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	privateKey := nostr.GeneratePrivateKey()
	signer, err := signing.NewLocalSigner(privateKey)
	require.NoError(t, err)

	emailSigner := email.NewEmailSigner(logger)
	ctx := context.Background()

	testEmail := &email.SignableEmail{
		Headers: map[string]string{
			"from":       "alice@coldforge.xyz",
			"to":         "bob@example.com",
			"date":       "Mon, 17 Feb 2026 10:00:00 +0000",
			"message-id": "test-sig@coldforge.xyz",
			"subject":    "Test",
		},
		Body: "Test body",
	}

	result, err := emailSigner.Sign(ctx, testEmail, signer)
	require.NoError(t, err)

	// Apply to header map
	headers := make(map[string]string)
	emailSigner.AddSignatureHeaders(headers, result)

	// Verify headers
	assert.Equal(t, signer.PublicKey(), headers[email.HeaderNostrPubkey])
	assert.Equal(t, result.Signature, headers[email.HeaderNostrSig])
	assert.Equal(t, result.SignedHeaders, headers[email.HeaderNostrSignedHeaders])
}

// TestCanonicalization tests that canonicalization is deterministic
func TestCanonicalization(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	privateKey := nostr.GeneratePrivateKey()
	signer, err := signing.NewLocalSigner(privateKey)
	require.NoError(t, err)

	emailSigner := email.NewEmailSigner(logger)
	ctx := context.Background()

	// Create two emails with different whitespace/formatting
	email1 := &email.SignableEmail{
		Headers: map[string]string{
			"from":       "alice@coldforge.xyz",
			"to":         "bob@example.com",
			"subject":    "Test  ",             // trailing spaces
			"date":       "Mon, 17 Feb 2026 10:00:00 +0000",
			"message-id": "test@example.com",
		},
		Body: "Hello\r\n\r\n",  // CRLF and trailing newlines
	}

	email2 := &email.SignableEmail{
		Headers: map[string]string{
			"from":       "alice@coldforge.xyz",
			"to":         "bob@example.com",
			"subject":    "Test",
			"date":       "Mon, 17 Feb 2026 10:00:00 +0000",
			"message-id": "test@example.com",
		},
		Body: "Hello",
	}

	result1, err := emailSigner.Sign(ctx, email1, signer)
	require.NoError(t, err)

	result2, err := emailSigner.Sign(ctx, email2, signer)
	require.NoError(t, err)

	// Canonical data should be the same after normalization
	assert.Equal(t, result1.CanonicalData, result2.CanonicalData,
		"Canonicalized emails should match despite whitespace differences")
}

// TestSignEmailConvenience tests the SignEmail convenience function
func TestSignEmailConvenience(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	privateKey := nostr.GeneratePrivateKey()
	signer, err := signing.NewLocalSigner(privateKey)
	require.NoError(t, err)

	ctx := context.Background()

	headers := map[string]string{
		"From":       "alice@coldforge.xyz",
		"To":         "bob@example.com",
		"Date":       "Mon, 17 Feb 2026 10:00:00 +0000",
		"Message-ID": "test@coldforge.xyz",
		"Subject":    "Test",
	}
	body := "Test body"

	result, err := email.SignEmail(ctx, signer, headers, body, logger)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.Signature)
	assert.Equal(t, signer.PublicKey(), result.Pubkey)
}
