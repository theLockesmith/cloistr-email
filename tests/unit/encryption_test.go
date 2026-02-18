package unit

import (
	"context"
	"fmt"
	"testing"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/encryption"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// MockEncryptor implements the Encryptor interface for testing
type MockEncryptor struct {
	EncryptFunc func(ctx context.Context, userPubkey, recipientPubkey, plaintext string) (string, error)
	DecryptFunc func(ctx context.Context, userPubkey, senderPubkey, ciphertext string) (string, error)
}

func (m *MockEncryptor) EncryptContent(ctx context.Context, userPubkey, recipientPubkey, plaintext string) (string, error) {
	if m.EncryptFunc != nil {
		return m.EncryptFunc(ctx, userPubkey, recipientPubkey, plaintext)
	}
	// Default: return base64-like "encrypted" content
	return "encrypted:" + plaintext, nil
}

func (m *MockEncryptor) DecryptContent(ctx context.Context, userPubkey, senderPubkey, ciphertext string) (string, error) {
	if m.DecryptFunc != nil {
		return m.DecryptFunc(ctx, userPubkey, senderPubkey, ciphertext)
	}
	// Default: strip "encrypted:" prefix
	if len(ciphertext) > 10 && ciphertext[:10] == "encrypted:" {
		return ciphertext[10:], nil
	}
	return ciphertext, nil
}

// MockKeyResolver implements the KeyResolver interface for testing
type MockKeyResolver struct {
	keys map[string]string
}

func NewMockKeyResolver() *MockKeyResolver {
	return &MockKeyResolver{
		keys: make(map[string]string),
	}
}

func (m *MockKeyResolver) SetKey(email, pubkey string) {
	m.keys[email] = pubkey
}

func (m *MockKeyResolver) ResolvePubkey(ctx context.Context, email string) (string, error) {
	pubkey, ok := m.keys[email]
	if !ok {
		return "", fmt.Errorf("no pubkey found for %s", email)
	}
	return pubkey, nil
}

// TestEmailEncryptor_EncryptEmailBody tests email body encryption
func TestEmailEncryptor_EncryptEmailBody(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	senderPubkey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	recipientPubkey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	tests := []struct {
		name         string
		plaintext    string
		encryptor    *MockEncryptor
		expectError  bool
		errorMsg     string
	}{
		{
			name:      "successful encryption",
			plaintext: "Hello, this is a secret message!",
			encryptor: &MockEncryptor{},
		},
		{
			name:      "empty body encryption",
			plaintext: "",
			encryptor: &MockEncryptor{},
		},
		{
			name:      "long body encryption",
			plaintext: string(make([]byte, 10000)),
			encryptor: &MockEncryptor{},
		},
		{
			name:      "encryption failure",
			plaintext: "test message",
			encryptor: &MockEncryptor{
				EncryptFunc: func(ctx context.Context, userPubkey, recipientPubkey, plaintext string) (string, error) {
					return "", fmt.Errorf("encryption failed: bunker unavailable")
				},
			},
			expectError: true,
			errorMsg:    "failed to encrypt email body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewMockKeyResolver()
			encryptor := encryption.NewEmailEncryptor(tt.encryptor, resolver, logger)

			ctx := context.Background()
			ciphertext, err := encryptor.EncryptEmailBody(ctx, senderPubkey, recipientPubkey, tt.plaintext)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, ciphertext)
			}
		})
	}
}

// TestEmailEncryptor_DecryptEmailBody tests email body decryption
func TestEmailEncryptor_DecryptEmailBody(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	recipientPubkey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	senderPubkey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	tests := []struct {
		name           string
		ciphertext     string
		encryptor      *MockEncryptor
		expectError    bool
		errorMsg       string
		expectedResult string
	}{
		{
			name:           "successful decryption",
			ciphertext:     "encrypted:Hello, this is a secret message!",
			encryptor:      &MockEncryptor{},
			expectedResult: "Hello, this is a secret message!",
		},
		{
			name:           "decryption with empty ciphertext",
			ciphertext:     "",
			encryptor:      &MockEncryptor{},
			expectedResult: "",
		},
		{
			name:       "decryption failure",
			ciphertext: "invalid-ciphertext",
			encryptor: &MockEncryptor{
				DecryptFunc: func(ctx context.Context, userPubkey, senderPubkey, ciphertext string) (string, error) {
					return "", fmt.Errorf("decryption failed: invalid ciphertext")
				},
			},
			expectError: true,
			errorMsg:    "failed to decrypt email body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewMockKeyResolver()
			encryptor := encryption.NewEmailEncryptor(tt.encryptor, resolver, logger)

			ctx := context.Background()
			plaintext, err := encryptor.DecryptEmailBody(ctx, recipientPubkey, senderPubkey, tt.ciphertext)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, plaintext)
			}
		})
	}
}

// TestEmailEncryptor_PrepareEncryptedEmail tests full email preparation
func TestEmailEncryptor_PrepareEncryptedEmail(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	senderPubkey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	recipientPubkey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	tests := []struct {
		name         string
		from         string
		to           string
		subject      string
		body         string
		setupKeys    func(*MockKeyResolver)
		encryptor    *MockEncryptor
		expectError  bool
		errorMsg     string
	}{
		{
			name:    "successful email preparation",
			from:    "bob@coldforge.xyz",
			to:      "alice@coldforge.xyz",
			subject: "Test Subject",
			body:    "Hello Alice!",
			setupKeys: func(r *MockKeyResolver) {
				r.SetKey("alice@coldforge.xyz", recipientPubkey)
			},
			encryptor: &MockEncryptor{},
		},
		{
			name:    "recipient key not found",
			from:    "bob@coldforge.xyz",
			to:      "unknown@example.com",
			subject: "Test Subject",
			body:    "Hello Unknown!",
			setupKeys: func(r *MockKeyResolver) {
				// No key set for recipient
			},
			encryptor:   &MockEncryptor{},
			expectError: true,
			errorMsg:    "failed to resolve recipient pubkey",
		},
		{
			name:    "encryption failure during preparation",
			from:    "bob@coldforge.xyz",
			to:      "alice@coldforge.xyz",
			subject: "Test Subject",
			body:    "Hello Alice!",
			setupKeys: func(r *MockKeyResolver) {
				r.SetKey("alice@coldforge.xyz", recipientPubkey)
			},
			encryptor: &MockEncryptor{
				EncryptFunc: func(ctx context.Context, userPubkey, recipientPubkey, plaintext string) (string, error) {
					return "", fmt.Errorf("bunker connection lost")
				},
			},
			expectError: true,
			errorMsg:    "failed to encrypt email body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewMockKeyResolver()
			if tt.setupKeys != nil {
				tt.setupKeys(resolver)
			}
			encryptor := encryption.NewEmailEncryptor(tt.encryptor, resolver, logger)

			ctx := context.Background()
			email, err := encryptor.PrepareEncryptedEmail(ctx, tt.from, tt.to, tt.subject, tt.body, senderPubkey)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				assert.Nil(t, email)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, email)

				assert.Equal(t, tt.from, email.From)
				assert.Equal(t, tt.to, email.To)
				assert.Equal(t, tt.subject, email.Subject)
				assert.True(t, email.IsEncrypted)
				assert.Equal(t, senderPubkey, email.SenderPubkey)
				assert.Equal(t, recipientPubkey, email.RecipientPubkey)
				assert.Equal(t, encryption.AlgorithmNIP44, email.Algorithm)
				assert.NotEmpty(t, email.Body)
			}
		})
	}
}

// TestEncryptedEmail_FormatEncryptedEmailHeaders tests header formatting
func TestEncryptedEmail_FormatEncryptedEmailHeaders(t *testing.T) {
	tests := []struct {
		name           string
		email          *encryption.EncryptedEmail
		expectedNil    bool
		expectedValues map[string]string
	}{
		{
			name: "encrypted email headers",
			email: &encryption.EncryptedEmail{
				IsEncrypted:     true,
				SenderPubkey:    "senderpubkey123",
				RecipientPubkey: "recipientpubkey456",
				Algorithm:       encryption.AlgorithmNIP44,
			},
			expectedNil: false,
			expectedValues: map[string]string{
				encryption.HeaderNostrEncrypted: "true",
				encryption.HeaderNostrSender:    "senderpubkey123",
				encryption.HeaderNostrRecipient: "recipientpubkey456",
				encryption.HeaderNostrAlgorithm: encryption.AlgorithmNIP44,
			},
		},
		{
			name: "unencrypted email returns nil",
			email: &encryption.EncryptedEmail{
				IsEncrypted: false,
			},
			expectedNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := tt.email.FormatEncryptedEmailHeaders()

			if tt.expectedNil {
				assert.Nil(t, headers)
			} else {
				require.NotNil(t, headers)
				for key, expectedValue := range tt.expectedValues {
					assert.Equal(t, expectedValue, headers[key], "Header %s mismatch", key)
				}
			}
		})
	}
}

// TestEncryptedEmail_FormatRawEmail tests raw RFC 5322 email formatting
func TestEncryptedEmail_FormatRawEmail(t *testing.T) {
	tests := []struct {
		name          string
		email         *encryption.EncryptedEmail
		checkContains []string
	}{
		{
			name: "encrypted email format",
			email: &encryption.EncryptedEmail{
				From:            "bob@coldforge.xyz",
				To:              "alice@coldforge.xyz",
				Subject:         "Test Subject",
				Body:            "encrypted-content",
				IsEncrypted:     true,
				SenderPubkey:    "senderpubkey",
				RecipientPubkey: "recipientpubkey",
				Algorithm:       encryption.AlgorithmNIP44,
			},
			checkContains: []string{
				"From: bob@coldforge.xyz",
				"To: alice@coldforge.xyz",
				"Subject: Test Subject",
				"MIME-Version: 1.0",
				"Content-Type: text/plain; charset=utf-8",
				"Content-Transfer-Encoding: base64",
				"X-Nostr-Encrypted: true",
				"X-Nostr-Sender: senderpubkey",
				"X-Nostr-Recipient: recipientpubkey",
				"X-Nostr-Algorithm: nip44",
			},
		},
		{
			name: "unencrypted email format",
			email: &encryption.EncryptedEmail{
				From:        "bob@coldforge.xyz",
				To:          "alice@coldforge.xyz",
				Subject:     "Plain Subject",
				Body:        "plain-content",
				IsEncrypted: false,
			},
			checkContains: []string{
				"From: bob@coldforge.xyz",
				"To: alice@coldforge.xyz",
				"Subject: Plain Subject",
				"MIME-Version: 1.0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := tt.email.FormatRawEmail()

			for _, expected := range tt.checkContains {
				assert.Contains(t, raw, expected, "Missing expected content: %s", expected)
			}
		})
	}
}

// TestParseEncryptedEmailHeaders tests header parsing from mail.Header
func TestParseEncryptedEmailHeaders(t *testing.T) {
	tests := []struct {
		name        string
		rawEmail    string
		isEncrypted bool
		sender      string
		recipient   string
		algorithm   string
	}{
		{
			name: "encrypted email headers",
			rawEmail: `From: bob@coldforge.xyz
To: alice@coldforge.xyz
Subject: Test
X-Nostr-Encrypted: true
X-Nostr-Sender: senderpubkey
X-Nostr-Recipient: recipientpubkey
X-Nostr-Algorithm: nip44

body content`,
			isEncrypted: true,
			sender:      "senderpubkey",
			recipient:   "recipientpubkey",
			algorithm:   "nip44",
		},
		{
			name: "unencrypted email headers",
			rawEmail: `From: bob@coldforge.xyz
To: alice@coldforge.xyz
Subject: Test

body content`,
			isEncrypted: false,
		},
		{
			name: "encrypted false header",
			rawEmail: `From: bob@coldforge.xyz
X-Nostr-Encrypted: false

body content`,
			isEncrypted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata, body, err := encryption.ParseRawEmail(tt.rawEmail)
			require.NoError(t, err)
			require.NotNil(t, metadata)
			assert.NotEmpty(t, body)

			assert.Equal(t, tt.isEncrypted, metadata.IsEncrypted)
			if tt.isEncrypted {
				assert.Equal(t, tt.sender, metadata.SenderPubkey)
				assert.Equal(t, tt.recipient, metadata.RecipientPubkey)
				assert.Equal(t, tt.algorithm, metadata.Algorithm)
			}
		})
	}
}

// TestParseRawEmail tests raw email parsing
func TestParseRawEmail(t *testing.T) {
	tests := []struct {
		name         string
		rawEmail     string
		expectError  bool
		errorMsg     string
		expectedBody string
	}{
		{
			name: "valid email parsing",
			rawEmail: `From: bob@coldforge.xyz
To: alice@coldforge.xyz
Subject: Test

Hello World`,
			expectedBody: "Hello World",
		},
		{
			name: "base64 encoded body",
			rawEmail: `From: bob@coldforge.xyz
To: alice@coldforge.xyz
Content-Transfer-Encoding: base64

SGVsbG8gV29ybGQ=`,
			expectedBody: "Hello World",
		},
		{
			name:        "invalid email format",
			rawEmail:    "not a valid email",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata, body, err := encryption.ParseRawEmail(tt.rawEmail)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				require.NotNil(t, metadata)
				assert.Equal(t, tt.expectedBody, body)
			}
		})
	}
}

// TestEmailEncryptor_DecryptEmail tests the DecryptEmail method
func TestEmailEncryptor_DecryptEmail(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	recipientPubkey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	senderPubkey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	tests := []struct {
		name           string
		metadata       *encryption.EncryptionMetadata
		encryptedBody  string
		encryptor      *MockEncryptor
		expectError    bool
		errorMsg       string
		expectedResult string
	}{
		{
			name: "successful decryption of encrypted email",
			metadata: &encryption.EncryptionMetadata{
				IsEncrypted:  true,
				SenderPubkey: senderPubkey,
				Algorithm:    encryption.AlgorithmNIP44,
			},
			encryptedBody:  "encrypted:secret message",
			encryptor:      &MockEncryptor{},
			expectedResult: "secret message",
		},
		{
			name: "unencrypted email returns body unchanged",
			metadata: &encryption.EncryptionMetadata{
				IsEncrypted: false,
			},
			encryptedBody:  "plain text message",
			encryptor:      &MockEncryptor{},
			expectedResult: "plain text message",
		},
		{
			name: "unsupported algorithm",
			metadata: &encryption.EncryptionMetadata{
				IsEncrypted:  true,
				SenderPubkey: senderPubkey,
				Algorithm:    "unknown-algorithm",
			},
			encryptedBody: "encrypted content",
			encryptor:     &MockEncryptor{},
			expectError:   true,
			errorMsg:      "unsupported encryption algorithm",
		},
		{
			name: "decryption failure",
			metadata: &encryption.EncryptionMetadata{
				IsEncrypted:  true,
				SenderPubkey: senderPubkey,
				Algorithm:    encryption.AlgorithmNIP44,
			},
			encryptedBody: "corrupted-data",
			encryptor: &MockEncryptor{
				DecryptFunc: func(ctx context.Context, userPubkey, senderPubkey, ciphertext string) (string, error) {
					return "", fmt.Errorf("decryption failed")
				},
			},
			expectError: true,
			errorMsg:    "failed to decrypt email body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewMockKeyResolver()
			encryptor := encryption.NewEmailEncryptor(tt.encryptor, resolver, logger)

			ctx := context.Background()
			result, err := encryptor.DecryptEmail(ctx, tt.metadata, recipientPubkey, tt.encryptedBody)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}
		})
	}
}

// TestHeaderConstants verifies header constants are correct
func TestHeaderConstants(t *testing.T) {
	assert.Equal(t, "X-Nostr-Encrypted", encryption.HeaderNostrEncrypted)
	assert.Equal(t, "X-Nostr-Sender", encryption.HeaderNostrSender)
	assert.Equal(t, "X-Nostr-Recipient", encryption.HeaderNostrRecipient)
	assert.Equal(t, "X-Nostr-Algorithm", encryption.HeaderNostrAlgorithm)
	assert.Equal(t, "nip44", encryption.AlgorithmNIP44)
}

// TestEmailEncryptor_ShortPubkeys tests that short pubkeys don't cause panics
func TestEmailEncryptor_ShortPubkeys(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	// Test with very short pubkeys (edge case for logging)
	shortSender := "abc"
	shortRecipient := "def"

	mockEncryptor := &MockEncryptor{}
	resolver := NewMockKeyResolver()
	encryptor := encryption.NewEmailEncryptor(mockEncryptor, resolver, logger)

	ctx := context.Background()

	// This should NOT panic even with short pubkeys
	t.Run("encrypt with short pubkeys does not panic", func(t *testing.T) {
		_, err := encryptor.EncryptEmailBody(ctx, shortSender, shortRecipient, "test message")
		// The encryption itself may fail, but we shouldn't panic
		assert.NoError(t, err)
	})

	t.Run("decrypt with short pubkeys does not panic", func(t *testing.T) {
		_, err := encryptor.DecryptEmailBody(ctx, shortRecipient, shortSender, "encrypted:test")
		// The decryption itself may fail, but we shouldn't panic
		assert.NoError(t, err)
	})

	t.Run("encrypt with empty pubkeys does not panic", func(t *testing.T) {
		_, err := encryptor.EncryptEmailBody(ctx, "", "", "test message")
		assert.NoError(t, err)
	})
}
