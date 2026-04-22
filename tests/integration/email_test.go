package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/encryption"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// getTestLogger returns a test logger
func getTestLogger(t *testing.T) *zap.Logger {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	return logger
}

// skipIfNoDatabase skips the test if DATABASE_URL is not set
func skipIfNoDatabase(t *testing.T) string {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("TEST_DATABASE_URL")
	}
	if dbURL == "" {
		t.Skip("Skipping integration test: DATABASE_URL or TEST_DATABASE_URL not set")
	}
	return dbURL
}

// TestEmailStorageIntegration tests email CRUD operations with real PostgreSQL
func TestEmailStorageIntegration(t *testing.T) {
	dbURL := skipIfNoDatabase(t)
	logger := getTestLogger(t)
	defer logger.Sync()

	// Connect to database
	db, err := storage.NewPostgres(dbURL, logger)
	require.NoError(t, err, "Failed to connect to database")
	defer db.Close()

	ctx := context.Background()

	// Run migrations
	err = db.Migrate(ctx)
	require.NoError(t, err, "Failed to run migrations")

	t.Run("create and retrieve email", func(t *testing.T) {
		email := &storage.Email{
			UserID:      "test-user-1",
			FromAddress: "alice@cloistr.xyz",
			ToAddress:   "bob@example.com",
			Subject:     "Test Email Subject",
			Body:        "Hello, this is a test email body.",
			IsEncrypted: false,
			Direction:   "sent",
			Folder:      "sent",
			Status:      "active",
		}

		err := db.CreateEmail(ctx, email)
		require.NoError(t, err, "Failed to create email")
		require.NotEmpty(t, email.ID, "Email ID should be set after creation")

		// Retrieve the email
		retrieved, err := db.GetEmail(ctx, email.ID)
		require.NoError(t, err, "Failed to retrieve email")
		require.NotNil(t, retrieved, "Retrieved email should not be nil")

		assert.Equal(t, email.ID, retrieved.ID)
		assert.Equal(t, email.FromAddress, retrieved.FromAddress)
		assert.Equal(t, email.ToAddress, retrieved.ToAddress)
		assert.Equal(t, email.Subject, retrieved.Subject)
		assert.Equal(t, email.Body, retrieved.Body)
		assert.Equal(t, email.IsEncrypted, retrieved.IsEncrypted)
		assert.Equal(t, email.Direction, retrieved.Direction)

		// Clean up
		err = db.DeleteEmail(ctx, email.ID)
		assert.NoError(t, err, "Failed to delete email")
	})

	t.Run("create and retrieve encrypted email", func(t *testing.T) {
		senderNpub := "npub1sender0000000000000000000000000000000000000000000000000"
		recipientNpub := "npub1recipient0000000000000000000000000000000000000000000000"

		email := &storage.Email{
			UserID:        "test-user-2",
			FromAddress:   "alice@cloistr.xyz",
			ToAddress:     "bob@cloistr.xyz",
			Subject:       "Encrypted Test Email",
			Body:          "AGVuY3J5cHRlZA==", // Simulated encrypted content
			IsEncrypted:   true,
			SenderNpub:    &senderNpub,
			RecipientNpub: &recipientNpub,
			Direction:     "sent",
			Folder:        "sent",
			Status:        "active",
		}

		err := db.CreateEmail(ctx, email)
		require.NoError(t, err, "Failed to create encrypted email")

		// Retrieve and verify encryption metadata
		retrieved, err := db.GetEmail(ctx, email.ID)
		require.NoError(t, err, "Failed to retrieve encrypted email")
		require.NotNil(t, retrieved)

		assert.True(t, retrieved.IsEncrypted, "Email should be marked as encrypted")
		require.NotNil(t, retrieved.SenderNpub, "Sender npub should be set")
		require.NotNil(t, retrieved.RecipientNpub, "Recipient npub should be set")
		assert.Equal(t, senderNpub, *retrieved.SenderNpub)
		assert.Equal(t, recipientNpub, *retrieved.RecipientNpub)

		// Clean up
		err = db.DeleteEmail(ctx, email.ID)
		assert.NoError(t, err)
	})

	t.Run("list emails with filters", func(t *testing.T) {
		userID := "test-user-list"

		// Create multiple emails
		for i := 0; i < 5; i++ {
			email := &storage.Email{
				UserID:      userID,
				FromAddress: "alice@cloistr.xyz",
				ToAddress:   "bob@example.com",
				Subject:     "List Test Email",
				Body:        "Test body",
				Direction:   "sent",
				Folder:      "sent",
				Status:      "active",
			}
			err := db.CreateEmail(ctx, email)
			require.NoError(t, err)
		}

		// List with filter
		filter := &storage.EmailFilter{
			Direction: "sent",
			Folder:    "sent",
		}
		opts := storage.ListOptions{
			Limit:  10,
			Offset: 0,
		}

		emails, total, err := db.ListEmails(ctx, userID, filter, opts)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, total, 5, "Should have at least 5 emails")
		assert.GreaterOrEqual(t, len(emails), 5, "Should return at least 5 emails")
	})

	t.Run("soft delete email", func(t *testing.T) {
		email := &storage.Email{
			UserID:      "test-user-delete",
			FromAddress: "alice@cloistr.xyz",
			ToAddress:   "bob@example.com",
			Subject:     "Delete Test",
			Body:        "To be deleted",
			Direction:   "sent",
			Folder:      "sent",
			Status:      "active",
		}

		err := db.CreateEmail(ctx, email)
		require.NoError(t, err)

		// Delete
		err = db.DeleteEmail(ctx, email.ID)
		require.NoError(t, err)

		// Should not be retrievable
		retrieved, err := db.GetEmail(ctx, email.ID)
		assert.NoError(t, err) // No error, but email should be nil
		assert.Nil(t, retrieved, "Deleted email should not be retrievable")
	})
}

// TestEncryptedEmailFlow tests the full encryption workflow
func TestEncryptedEmailFlow(t *testing.T) {
	logger := getTestLogger(t)
	defer logger.Sync()

	// These tests use mocks to test the encryption flow without NIP-46 bunker
	t.Run("prepare encrypted email with key resolution", func(t *testing.T) {
		// Create mock encryptor
		mockEncryptor := &mockEncryptor{}
		mockResolver := newMockKeyResolver()

		// Setup recipient key
		recipientEmail := "alice@cloistr.xyz"
		recipientPubkey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
		mockResolver.SetKey(recipientEmail, recipientPubkey)

		encryptor := encryption.NewEmailEncryptor(mockEncryptor, mockResolver, logger)

		ctx := context.Background()
		senderPubkey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

		email, err := encryptor.PrepareEncryptedEmail(
			ctx,
			"bob@cloistr.xyz",
			recipientEmail,
			"Secret Subject",
			"This is a secret message",
			senderPubkey,
		)

		require.NoError(t, err)
		require.NotNil(t, email)

		assert.True(t, email.IsEncrypted)
		assert.Equal(t, senderPubkey, email.SenderPubkey)
		assert.Equal(t, recipientPubkey, email.RecipientPubkey)
		assert.Equal(t, encryption.AlgorithmNIP44, email.Algorithm)
		assert.NotEmpty(t, email.Body, "Encrypted body should not be empty")
	})

	t.Run("decrypt email returns correct plaintext", func(t *testing.T) {
		mockEncryptor := &mockEncryptor{}
		mockResolver := newMockKeyResolver()

		encryptor := encryption.NewEmailEncryptor(mockEncryptor, mockResolver, logger)

		ctx := context.Background()
		recipientPubkey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
		senderPubkey := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

		metadata := &encryption.EncryptionMetadata{
			IsEncrypted:  true,
			SenderPubkey: senderPubkey,
			Algorithm:    encryption.AlgorithmNIP44,
		}

		// Mock encryptor returns content after "encrypted:" prefix
		ciphertext := "encrypted:Hello, this is the secret message!"
		plaintext, err := encryptor.DecryptEmail(ctx, metadata, recipientPubkey, ciphertext)

		require.NoError(t, err)
		assert.Equal(t, "Hello, this is the secret message!", plaintext)
	})

	t.Run("unencrypted email returns body unchanged", func(t *testing.T) {
		mockEncryptor := &mockEncryptor{}
		mockResolver := newMockKeyResolver()

		encryptor := encryption.NewEmailEncryptor(mockEncryptor, mockResolver, logger)

		ctx := context.Background()
		recipientPubkey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

		metadata := &encryption.EncryptionMetadata{
			IsEncrypted: false,
		}

		body := "Plain text message"
		result, err := encryptor.DecryptEmail(ctx, metadata, recipientPubkey, body)

		require.NoError(t, err)
		assert.Equal(t, body, result)
	})

	t.Run("unsupported algorithm returns error", func(t *testing.T) {
		mockEncryptor := &mockEncryptor{}
		mockResolver := newMockKeyResolver()

		encryptor := encryption.NewEmailEncryptor(mockEncryptor, mockResolver, logger)

		ctx := context.Background()
		recipientPubkey := "1234567890abcdef"

		metadata := &encryption.EncryptionMetadata{
			IsEncrypted:  true,
			SenderPubkey: "senderpubkey",
			Algorithm:    "unsupported-algo",
		}

		_, err := encryptor.DecryptEmail(ctx, metadata, recipientPubkey, "ciphertext")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported encryption algorithm")
	})
}

// TestRawEmailFormatting tests RFC 5322 email formatting with encryption headers
func TestRawEmailFormatting(t *testing.T) {
	t.Run("encrypted email includes X-Nostr headers", func(t *testing.T) {
		email := &encryption.EncryptedEmail{
			From:            "bob@cloistr.xyz",
			To:              "alice@cloistr.xyz",
			Subject:         "Encrypted Message",
			Body:            "ZW5jcnlwdGVkLWNvbnRlbnQ=",
			IsEncrypted:     true,
			SenderPubkey:    "senderpubkey123",
			RecipientPubkey: "recipientpubkey456",
			Algorithm:       encryption.AlgorithmNIP44,
		}

		raw := email.FormatRawEmail()

		assert.Contains(t, raw, "From: bob@cloistr.xyz")
		assert.Contains(t, raw, "To: alice@cloistr.xyz")
		assert.Contains(t, raw, "Subject: Encrypted Message")
		assert.Contains(t, raw, "X-Nostr-Encrypted: true")
		assert.Contains(t, raw, "X-Nostr-Sender: senderpubkey123")
		assert.Contains(t, raw, "X-Nostr-Recipient: recipientpubkey456")
		assert.Contains(t, raw, "X-Nostr-Algorithm: nip44")
		assert.Contains(t, raw, "MIME-Version: 1.0")
	})

	t.Run("unencrypted email has no X-Nostr headers", func(t *testing.T) {
		email := &encryption.EncryptedEmail{
			From:        "bob@cloistr.xyz",
			To:          "alice@example.com",
			Subject:     "Plain Message",
			Body:        "Hello, this is a plain message.",
			IsEncrypted: false,
		}

		raw := email.FormatRawEmail()

		assert.Contains(t, raw, "From: bob@cloistr.xyz")
		assert.Contains(t, raw, "To: alice@example.com")
		assert.Contains(t, raw, "Subject: Plain Message")
		assert.NotContains(t, raw, "X-Nostr-Encrypted:")
		assert.NotContains(t, raw, "X-Nostr-Sender:")
	})
}

// TestRawEmailParsing tests parsing raw emails with encryption metadata
func TestRawEmailParsing(t *testing.T) {
	t.Run("parse encrypted email headers", func(t *testing.T) {
		rawEmail := `From: bob@cloistr.xyz
To: alice@cloistr.xyz
Subject: Encrypted Test
X-Nostr-Encrypted: true
X-Nostr-Sender: senderpubkey123
X-Nostr-Recipient: recipientpubkey456
X-Nostr-Algorithm: nip44

encrypted body content`

		metadata, body, err := encryption.ParseRawEmail(rawEmail)
		require.NoError(t, err)
		require.NotNil(t, metadata)

		assert.True(t, metadata.IsEncrypted)
		assert.Equal(t, "senderpubkey123", metadata.SenderPubkey)
		assert.Equal(t, "recipientpubkey456", metadata.RecipientPubkey)
		assert.Equal(t, "nip44", metadata.Algorithm)
		assert.Equal(t, "encrypted body content", body)
	})

	t.Run("parse unencrypted email", func(t *testing.T) {
		rawEmail := `From: bob@cloistr.xyz
To: alice@example.com
Subject: Plain Message

Hello World`

		metadata, body, err := encryption.ParseRawEmail(rawEmail)
		require.NoError(t, err)
		require.NotNil(t, metadata)

		assert.False(t, metadata.IsEncrypted)
		assert.Empty(t, metadata.SenderPubkey)
		assert.Equal(t, "Hello World", body)
	})

	t.Run("parse base64 encoded body", func(t *testing.T) {
		rawEmail := `From: bob@cloistr.xyz
To: alice@cloistr.xyz
Content-Transfer-Encoding: base64

SGVsbG8gV29ybGQ=`

		metadata, body, err := encryption.ParseRawEmail(rawEmail)
		require.NoError(t, err)
		require.NotNil(t, metadata)

		assert.Equal(t, "Hello World", body)
	})
}

// TestEncryptionModeHandling tests different encryption modes
func TestEncryptionModeHandling(t *testing.T) {
	t.Run("none mode returns plaintext", func(t *testing.T) {
		mode := encryption.ModeNone
		assert.Equal(t, "none", string(mode))
	})

	t.Run("server mode constant", func(t *testing.T) {
		mode := encryption.ModeServerSide
		assert.Equal(t, "server", string(mode))
	})

	t.Run("client mode constant", func(t *testing.T) {
		mode := encryption.ModeClientSide
		assert.Equal(t, "client", string(mode))
	})
}

// TestNIP05CacheIntegration tests NIP-05 cache with real database
func TestNIP05CacheIntegration(t *testing.T) {
	dbURL := skipIfNoDatabase(t)
	logger := getTestLogger(t)
	defer logger.Sync()

	db, err := storage.NewPostgres(dbURL, logger)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	err = db.Migrate(ctx)
	require.NoError(t, err)

	t.Run("cache NIP-05 lookup result", func(t *testing.T) {
		email := "alice@cloistr.xyz"
		npub := "npub1alice00000000000000000000000000000000000000000000000000"
		ttl := 24 * time.Hour

		err := db.CacheNIP05(ctx, email, &npub, ttl)
		require.NoError(t, err)

		// Retrieve from cache
		cached, err := db.GetCachedNIP05(ctx, email)
		require.NoError(t, err)
		require.NotNil(t, cached)

		assert.Equal(t, email, cached.Email)
		assert.NotNil(t, cached.Npub)
		assert.Equal(t, npub, *cached.Npub)
		assert.True(t, cached.Valid)
	})

	t.Run("cache negative result", func(t *testing.T) {
		email := "unknown@example.com"
		ttl := 1 * time.Hour

		err := db.CacheNIP05(ctx, email, nil, ttl)
		require.NoError(t, err)

		cached, err := db.GetCachedNIP05(ctx, email)
		require.NoError(t, err)
		require.NotNil(t, cached)

		assert.Equal(t, email, cached.Email)
		assert.Nil(t, cached.Npub)
	})
}

// Mock implementations for testing

type mockEncryptor struct {
	encryptFunc func(ctx context.Context, userPubkey, recipientPubkey, plaintext string) (string, error)
	decryptFunc func(ctx context.Context, userPubkey, senderPubkey, ciphertext string) (string, error)
}

func (m *mockEncryptor) EncryptContent(ctx context.Context, userPubkey, recipientPubkey, plaintext string) (string, error) {
	if m.encryptFunc != nil {
		return m.encryptFunc(ctx, userPubkey, recipientPubkey, plaintext)
	}
	return "encrypted:" + plaintext, nil
}

func (m *mockEncryptor) DecryptContent(ctx context.Context, userPubkey, senderPubkey, ciphertext string) (string, error) {
	if m.decryptFunc != nil {
		return m.decryptFunc(ctx, userPubkey, senderPubkey, ciphertext)
	}
	if len(ciphertext) > 10 && ciphertext[:10] == "encrypted:" {
		return ciphertext[10:], nil
	}
	return ciphertext, nil
}

type mockKeyResolver struct {
	keys map[string]string
}

func newMockKeyResolver() *mockKeyResolver {
	return &mockKeyResolver{
		keys: make(map[string]string),
	}
}

func (m *mockKeyResolver) SetKey(email, pubkey string) {
	m.keys[email] = pubkey
}

func (m *mockKeyResolver) ResolvePubkey(ctx context.Context, email string) (string, error) {
	pubkey, ok := m.keys[email]
	if !ok {
		return "", nil
	}
	return pubkey, nil
}
