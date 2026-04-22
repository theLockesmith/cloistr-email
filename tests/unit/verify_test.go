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

// MockNIP05Resolver for testing
type MockNIP05Resolver struct {
	results map[string]string
}

func NewMockNIP05Resolver() *MockNIP05Resolver {
	return &MockNIP05Resolver{
		results: make(map[string]string),
	}
}

func (m *MockNIP05Resolver) AddMapping(email, pubkey string) {
	m.results[email] = pubkey
}

func (m *MockNIP05Resolver) ResolvePubkey(ctx context.Context, emailAddr string) (string, error) {
	if pubkey, ok := m.results[emailAddr]; ok {
		return pubkey, nil
	}
	return "", nil
}

// TestVerifyUnsignedEmail tests verification of unsigned emails
func TestVerifyUnsignedEmail(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	verifier := email.NewEmailVerifier(nil, logger)
	ctx := context.Background()

	unsignedEmail := &email.VerifiableEmail{
		Headers: map[string]string{
			"from":    "alice@example.com",
			"to":      "bob@example.com",
			"subject": "Test",
		},
		Body:        "Hello",
		FromAddress: "alice@example.com",
	}

	result := verifier.Verify(ctx, unsignedEmail)

	assert.False(t, result.Signed)
	assert.False(t, result.Valid)
	assert.Contains(t, result.Reason, "no Nostr signature headers")
}

// TestVerifySignedEmail tests verification of a properly signed email
func TestVerifySignedEmail(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	// Create signer
	privateKey := nostr.GeneratePrivateKey()
	signer, err := signing.NewLocalSigner(privateKey)
	require.NoError(t, err)

	ctx := context.Background()

	// Sign an email
	headers := map[string]string{
		"from":       "alice@cloistr.xyz",
		"to":         "bob@example.com",
		"date":       "Mon, 17 Feb 2026 10:00:00 +0000",
		"message-id": "test-verify@cloistr.xyz",
		"subject":    "Test Email",
	}
	body := "Hello Bob!"

	signResult, err := email.SignEmail(ctx, signer, headers, body, logger)
	require.NoError(t, err)

	// Verify the email
	verifier := email.NewEmailVerifier(nil, logger)

	verifiableEmail := &email.VerifiableEmail{
		Headers:            headers,
		Body:               body,
		NostrPubkey:        signResult.Pubkey,
		NostrSig:           signResult.Signature,
		NostrSignedHeaders: signResult.SignedHeaders,
		FromAddress:        "alice@cloistr.xyz",
	}

	result := verifier.Verify(ctx, verifiableEmail)

	assert.True(t, result.Signed)
	assert.True(t, result.Valid)
	assert.Equal(t, signer.PublicKey(), result.Pubkey)
	assert.Empty(t, result.Reason)
}

// TestVerifyTamperedEmail tests that tampered emails fail verification
func TestVerifyTamperedEmail(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	privateKey := nostr.GeneratePrivateKey()
	signer, err := signing.NewLocalSigner(privateKey)
	require.NoError(t, err)

	ctx := context.Background()

	headers := map[string]string{
		"from":       "alice@cloistr.xyz",
		"to":         "bob@example.com",
		"date":       "Mon, 17 Feb 2026 10:00:00 +0000",
		"message-id": "test-tamper@cloistr.xyz",
		"subject":    "Test Email",
	}
	body := "Original message"

	signResult, err := email.SignEmail(ctx, signer, headers, body, logger)
	require.NoError(t, err)

	tests := []struct {
		name           string
		modifyEmail    func(*email.VerifiableEmail)
		expectedReason string
	}{
		{
			name: "tampered body",
			modifyEmail: func(e *email.VerifiableEmail) {
				e.Body = "Tampered message"
			},
			expectedReason: "signature verification failed",
		},
		{
			name: "tampered subject",
			modifyEmail: func(e *email.VerifiableEmail) {
				e.Headers["subject"] = "Tampered Subject"
			},
			expectedReason: "signature verification failed",
		},
		{
			name: "tampered from address",
			modifyEmail: func(e *email.VerifiableEmail) {
				e.Headers["from"] = "mallory@evil.com"
			},
			expectedReason: "signature verification failed",
		},
		{
			name: "wrong pubkey",
			modifyEmail: func(e *email.VerifiableEmail) {
				otherKey := nostr.GeneratePrivateKey()
				otherPubkey, _ := nostr.GetPublicKey(otherKey)
				e.NostrPubkey = otherPubkey
			},
			expectedReason: "signature verification failed",
		},
		{
			name: "invalid signature hex",
			modifyEmail: func(e *email.VerifiableEmail) {
				e.NostrSig = "invalid_signature"
			},
			expectedReason: "invalid signature",
		},
		{
			name: "truncated signature",
			modifyEmail: func(e *email.VerifiableEmail) {
				e.NostrSig = signResult.Signature[:64] // Half the signature
			},
			expectedReason: "invalid signature length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := email.NewEmailVerifier(nil, logger)

			// Create fresh verifiable email
			verifiableEmail := &email.VerifiableEmail{
				Headers: map[string]string{
					"from":       headers["from"],
					"to":         headers["to"],
					"date":       headers["date"],
					"message-id": headers["message-id"],
					"subject":    headers["subject"],
				},
				Body:               body,
				NostrPubkey:        signResult.Pubkey,
				NostrSig:           signResult.Signature,
				NostrSignedHeaders: signResult.SignedHeaders,
				FromAddress:        "alice@cloistr.xyz",
			}

			// Apply modification
			tt.modifyEmail(verifiableEmail)

			result := verifier.Verify(ctx, verifiableEmail)

			assert.True(t, result.Signed)
			assert.False(t, result.Valid)
			assert.Contains(t, result.Reason, tt.expectedReason)
		})
	}
}

// TestVerifyWithNIP05 tests NIP-05 verification
func TestVerifyWithNIP05(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	privateKey := nostr.GeneratePrivateKey()
	signer, err := signing.NewLocalSigner(privateKey)
	require.NoError(t, err)

	ctx := context.Background()

	headers := map[string]string{
		"from":       "alice@cloistr.xyz",
		"to":         "bob@example.com",
		"date":       "Mon, 17 Feb 2026 10:00:00 +0000",
		"message-id": "test-nip05@cloistr.xyz",
		"subject":    "Test",
	}
	body := "Hello"

	signResult, err := email.SignEmail(ctx, signer, headers, body, logger)
	require.NoError(t, err)

	tests := []struct {
		name            string
		setupResolver   func(*MockNIP05Resolver)
		expectNIP05Valid bool
	}{
		{
			name: "NIP-05 matches",
			setupResolver: func(r *MockNIP05Resolver) {
				r.AddMapping("alice@cloistr.xyz", signer.PublicKey())
			},
			expectNIP05Valid: true,
		},
		{
			name: "NIP-05 mismatch",
			setupResolver: func(r *MockNIP05Resolver) {
				otherKey := nostr.GeneratePrivateKey()
				otherPubkey, _ := nostr.GetPublicKey(otherKey)
				r.AddMapping("alice@cloistr.xyz", otherPubkey)
			},
			expectNIP05Valid: false,
		},
		{
			name: "NIP-05 not found",
			setupResolver: func(r *MockNIP05Resolver) {
				// No mapping
			},
			expectNIP05Valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewMockNIP05Resolver()
			tt.setupResolver(resolver)

			verifier := email.NewEmailVerifier(resolver, logger)

			verifiableEmail := &email.VerifiableEmail{
				Headers:            headers,
				Body:               body,
				NostrPubkey:        signResult.Pubkey,
				NostrSig:           signResult.Signature,
				NostrSignedHeaders: signResult.SignedHeaders,
				FromAddress:        "alice@cloistr.xyz",
			}

			result := verifier.Verify(ctx, verifiableEmail)

			// Signature should always be valid regardless of NIP-05
			assert.True(t, result.Signed)
			assert.True(t, result.Valid)
			assert.Equal(t, tt.expectNIP05Valid, result.NIP05Verified)
			assert.Equal(t, "alice@cloistr.xyz", result.NIP05Address)
		})
	}
}

// TestVerifyEmailConvenience tests the VerifyEmail convenience function
func TestVerifyEmailConvenience(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	privateKey := nostr.GeneratePrivateKey()
	signer, err := signing.NewLocalSigner(privateKey)
	require.NoError(t, err)

	ctx := context.Background()

	headers := map[string]string{
		"From":       "alice@cloistr.xyz",
		"To":         "bob@example.com",
		"Date":       "Mon, 17 Feb 2026 10:00:00 +0000",
		"Message-ID": "test-conv@cloistr.xyz",
		"Subject":    "Test",
	}
	body := "Test body"

	// Sign
	signResult, err := email.SignEmail(ctx, signer, headers, body, logger)
	require.NoError(t, err)

	// Add signature headers
	headers["X-Nostr-Pubkey"] = signResult.Pubkey
	headers["X-Nostr-Sig"] = signResult.Signature
	headers["X-Nostr-Signed-Headers"] = signResult.SignedHeaders

	// Verify
	result := email.VerifyEmail(ctx, headers, body, nil, logger)

	assert.True(t, result.Signed)
	assert.True(t, result.Valid)
	assert.Equal(t, signer.PublicKey(), result.Pubkey)
}

// TestExtractEmailAddress tests email address extraction from From header
func TestExtractEmailAddress(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	// This is tested implicitly through VerifyEmail
	// We verify it handles various From header formats

	privateKey := nostr.GeneratePrivateKey()
	signer, err := signing.NewLocalSigner(privateKey)
	require.NoError(t, err)

	ctx := context.Background()

	tests := []struct {
		name        string
		fromHeader  string
		expectedAddr string
	}{
		{
			name:         "simple email",
			fromHeader:   "alice@cloistr.xyz",
			expectedAddr: "alice@cloistr.xyz",
		},
		{
			name:         "name with angle brackets",
			fromHeader:   "Alice <alice@cloistr.xyz>",
			expectedAddr: "alice@cloistr.xyz",
		},
		{
			name:         "quoted name with angle brackets",
			fromHeader:   "\"Alice Smith\" <alice@cloistr.xyz>",
			expectedAddr: "alice@cloistr.xyz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewMockNIP05Resolver()
			resolver.AddMapping(tt.expectedAddr, signer.PublicKey())

			// Sign with normalized address
			headers := map[string]string{
				"from":       tt.expectedAddr,
				"to":         "bob@example.com",
				"date":       "Mon, 17 Feb 2026 10:00:00 +0000",
				"message-id": "test@cloistr.xyz",
				"subject":    "Test",
			}
			body := "Test"

			signResult, err := email.SignEmail(ctx, signer, headers, body, logger)
			require.NoError(t, err)

			// Verify with original From header
			verifyHeaders := map[string]string{
				"from":                   tt.expectedAddr, // Use same for signature
				"to":                     "bob@example.com",
				"date":                   "Mon, 17 Feb 2026 10:00:00 +0000",
				"message-id":             "test@cloistr.xyz",
				"subject":                "Test",
				"x-nostr-pubkey":         signResult.Pubkey,
				"x-nostr-sig":            signResult.Signature,
				"x-nostr-signed-headers": signResult.SignedHeaders,
			}

			verifier := email.NewEmailVerifier(resolver, logger)
			verifiableEmail := &email.VerifiableEmail{
				Headers:            verifyHeaders,
				Body:               body,
				NostrPubkey:        signResult.Pubkey,
				NostrSig:           signResult.Signature,
				NostrSignedHeaders: signResult.SignedHeaders,
				FromAddress:        tt.fromHeader,
			}

			result := verifier.Verify(ctx, verifiableEmail)

			assert.True(t, result.Valid)
			assert.True(t, result.NIP05Verified)
		})
	}
}
