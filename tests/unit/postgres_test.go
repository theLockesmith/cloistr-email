package unit

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// testPubkey is a valid 64-char lowercase hex string used as a stand-in owner
// pubkey in unit tests that need a MailboxPubkey value but don't touch the DB.
const testPubkey = "0000000000000000000000000000000000000000000000000000000000000001"

// ============================================================================
// Model Tests
// ============================================================================

// TestEmailModel tests the Email struct
func TestEmailModel(t *testing.T) {
	now := time.Now()
	messageID := "msg-123"
	senderNpub := "npub1sender..."
	recipientNpub := "npub1recipient..."

	email := &storage.Email{
		ID:            "email-uuid",
		MailboxPubkey: testPubkey,
		MessageID:     &messageID,
		FromAddress:   "sender@example.com",
		ToAddress:     "recipient@example.com",
		Subject:       "Test Subject",
		Body:          "Test Body",
		IsEncrypted:   true,
		SenderNpub:    &senderNpub,
		RecipientNpub: &recipientNpub,
		Direction:     "sent",
		Status:        "active",
		Folder:        "Sent",
		Labels:        []string{"important", "work"},
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	assert.Equal(t, "email-uuid", email.ID)
	assert.Equal(t, testPubkey, email.MailboxPubkey)
	assert.Equal(t, "msg-123", *email.MessageID)
	assert.Equal(t, "sender@example.com", email.FromAddress)
	assert.Equal(t, "recipient@example.com", email.ToAddress)
	assert.Equal(t, "Test Subject", email.Subject)
	assert.True(t, email.IsEncrypted)
	assert.Equal(t, "sent", email.Direction)
	assert.Equal(t, "active", email.Status)
	assert.Equal(t, "Sent", email.Folder)
	assert.Len(t, email.Labels, 2)
}

// TestContactModel tests the Contact struct
func TestContactModel(t *testing.T) {
	now := time.Now()
	name := "Alice"
	npub := "npub1alice..."
	notes := "Test contact"
	org := "Test Org"
	phone := "+1234567890"

	contact := &storage.Contact{
		ID:            "contact-uuid",
		MailboxPubkey: testPubkey,
		Email:         "alice@example.com",
		Name:          &name,
		Npub:          &npub,
		Notes:         &notes,
		Organization:  &org,
		Phone:         &phone,
		AlwaysEncrypt: true,
		Blocked:       false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	assert.Equal(t, "contact-uuid", contact.ID)
	assert.Equal(t, testPubkey, contact.MailboxPubkey)
	assert.Equal(t, "alice@example.com", contact.Email)
	assert.Equal(t, "Alice", *contact.Name)
	assert.Equal(t, "npub1alice...", *contact.Npub)
	assert.True(t, contact.AlwaysEncrypt)
	assert.False(t, contact.Blocked)
}

// TestAttachmentModel tests the Attachment struct
func TestAttachmentModel(t *testing.T) {
	now := time.Now()
	contentType := "application/pdf"
	size := int64(1024)
	sha256 := "abc123..."
	url := "https://blossom.example.com/abc123"

	attachment := &storage.Attachment{
		ID:            "attachment-uuid",
		EmailID:       "email-uuid",
		Filename:      "document.pdf",
		ContentType:   &contentType,
		SizeBytes:     &size,
		BlossomSHA256: &sha256,
		BlossomURL:    &url,
		CreatedAt:     now,
	}

	assert.Equal(t, "attachment-uuid", attachment.ID)
	assert.Equal(t, "email-uuid", attachment.EmailID)
	assert.Equal(t, "document.pdf", attachment.Filename)
	assert.Equal(t, "application/pdf", *attachment.ContentType)
	assert.Equal(t, int64(1024), *attachment.SizeBytes)
}

// TestNIP05CacheEntry tests the NIP05CacheEntry struct
func TestNIP05CacheEntry(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)
	npub := "npub1test..."

	entry := &storage.NIP05CacheEntry{
		ID:        "cache-uuid",
		Email:     "test@example.com",
		Npub:      &npub,
		CachedAt:  now,
		ExpiresAt: &expiresAt,
		Valid:     true,
	}

	assert.Equal(t, "cache-uuid", entry.ID)
	assert.Equal(t, "test@example.com", entry.Email)
	assert.Equal(t, "npub1test...", *entry.Npub)
	assert.True(t, entry.Valid)
}

// TestAuditLogEntry tests the AuditLogEntry struct
func TestAuditLogEntry(t *testing.T) {
	now := time.Now()
	pubkey := testPubkey
	resourceType := "email"
	resourceID := "email-uuid"
	ipAddress := "192.168.1.1"
	userAgent := "Mozilla/5.0"

	entry := &storage.AuditLogEntry{
		ID:           "audit-uuid",
		MailboxPubkey: &pubkey,
		Action:       "send_email",
		ResourceType: &resourceType,
		ResourceID:   &resourceID,
		Details: map[string]interface{}{
			"recipient": "alice@example.com",
			"encrypted": true,
		},
		IPAddress: &ipAddress,
		UserAgent: &userAgent,
		CreatedAt: now,
	}

	assert.Equal(t, "audit-uuid", entry.ID)
	assert.Equal(t, testPubkey, *entry.MailboxPubkey)
	assert.Equal(t, "send_email", entry.Action)
	assert.Equal(t, "email", *entry.ResourceType)
	assert.Equal(t, "email-uuid", *entry.ResourceID)
	assert.Equal(t, "alice@example.com", entry.Details["recipient"])
	assert.Equal(t, true, entry.Details["encrypted"])
}

// ============================================================================
// ListOptions Tests
// ============================================================================

// TestDefaultListOptions tests default pagination options
func TestDefaultListOptions(t *testing.T) {
	opts := storage.DefaultListOptions()

	assert.Equal(t, 50, opts.Limit)
	assert.Equal(t, 0, opts.Offset)
	assert.Equal(t, "created_at", opts.OrderBy)
	assert.True(t, opts.OrderDesc)
}

// TestListOptions tests custom pagination options
func TestListOptions(t *testing.T) {
	opts := storage.ListOptions{
		Limit:     25,
		Offset:    100,
		OrderBy:   "name",
		OrderDesc: false,
	}

	assert.Equal(t, 25, opts.Limit)
	assert.Equal(t, 100, opts.Offset)
	assert.Equal(t, "name", opts.OrderBy)
	assert.False(t, opts.OrderDesc)
}

// TestEmailFilter tests email filter options
func TestEmailFilter(t *testing.T) {
	unread := true
	filter := storage.EmailFilter{
		Direction: "received",
		Status:    "active",
		Folder:    "INBOX",
		Labels:    []string{"important"},
		Unread:    &unread,
		Search:    "invoice",
	}

	assert.Equal(t, "received", filter.Direction)
	assert.Equal(t, "active", filter.Status)
	assert.Equal(t, "INBOX", filter.Folder)
	assert.Len(t, filter.Labels, 1)
	assert.True(t, *filter.Unread)
	assert.Equal(t, "invoice", filter.Search)
}

// ============================================================================
// PostgreSQL with Mock Tests
// ============================================================================

// TestPostgresEnsureMailbox tests mailbox upsert with mock
func TestPostgresEnsureMailbox(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	logger, _ := zap.NewDevelopment()
	postgres := newTestPostgres(db, logger)

	now := time.Now()

	t.Run("ensure creates new mailbox", func(t *testing.T) {
		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO mailboxes`)).
			WithArgs(testPubkey).
			WillReturnRows(sqlmock.NewRows([]string{"pubkey", "display_name", "created_at", "updated_at", "deleted_at"}).
				AddRow(testPubkey, nil, now, now, nil))

		mb, err := postgres.EnsureMailbox(context.Background(), testPubkey)
		assert.NoError(t, err)
		require.NotNil(t, mb)
		assert.Equal(t, testPubkey, mb.Pubkey)
		assert.Equal(t, now, mb.CreatedAt)
	})
}

// TestPostgresGetMailboxByPubkey tests mailbox retrieval with mock
func TestPostgresGetMailboxByPubkey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	logger, _ := zap.NewDevelopment()
	postgres := newTestPostgres(db, logger)

	now := time.Now()

	t.Run("mailbox found", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{
			"pubkey", "display_name", "created_at", "updated_at", "deleted_at",
		}).AddRow(testPubkey, nil, now, now, nil)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT pubkey, display_name, created_at, updated_at, deleted_at FROM mailboxes`)).
			WithArgs(testPubkey).
			WillReturnRows(rows)

		mb, err := postgres.GetMailboxByPubkey(context.Background(), testPubkey)
		assert.NoError(t, err)
		require.NotNil(t, mb)
		assert.Equal(t, testPubkey, mb.Pubkey)
	})

	t.Run("mailbox not found", func(t *testing.T) {
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT pubkey, display_name, created_at, updated_at, deleted_at FROM mailboxes`)).
			WithArgs(testPubkey).
			WillReturnError(sql.ErrNoRows)

		mb, err := postgres.GetMailboxByPubkey(context.Background(), testPubkey)
		assert.NoError(t, err)
		assert.Nil(t, mb)
	})
}

// TestPostgresCreateEmail tests email creation with mock
func TestPostgresCreateEmail(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	logger, _ := zap.NewDevelopment()
	postgres := newTestPostgres(db, logger)

	now := time.Now()

	t.Run("successful email creation", func(t *testing.T) {
		email := &storage.Email{
			MailboxPubkey: testPubkey,
			FromAddress:   "sender@example.com",
			ToAddress:     "recipient@example.com",
			Subject:       "Test Subject",
			Body:          "Test Body",
			IsEncrypted:   true,
		}

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO emails`)).
			WithArgs(
				sqlmock.AnyArg(), email.MailboxPubkey, nil, email.FromAddress, email.ToAddress,
				nil, nil, email.Subject, email.Body, nil,
				email.IsEncrypted, nil, nil, nil, nil,
				"sent", "active", "Sent", pq.Array([]string(nil)), nil, nil,
			).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).
				AddRow(now, now))

		err := postgres.CreateEmail(context.Background(), email)
		assert.NoError(t, err)
		assert.NotEmpty(t, email.ID)
		assert.Equal(t, "sent", email.Direction)
		assert.Equal(t, "active", email.Status)
		assert.Equal(t, "Sent", email.Folder)
	})

	t.Run("email with direction received", func(t *testing.T) {
		email := &storage.Email{
			MailboxPubkey: testPubkey,
			FromAddress:   "sender@example.com",
			ToAddress:     "recipient@example.com",
			Subject:       "Test Subject",
			Body:          "Test Body",
			Direction:     "received",
		}

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO emails`)).
			WithArgs(
				sqlmock.AnyArg(), email.MailboxPubkey, nil, email.FromAddress, email.ToAddress,
				nil, nil, email.Subject, email.Body, nil,
				false, nil, nil, nil, nil,
				"received", "active", "INBOX", pq.Array([]string(nil)), nil, nil,
			).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).
				AddRow(now, now))

		err := postgres.CreateEmail(context.Background(), email)
		assert.NoError(t, err)
		assert.Equal(t, "INBOX", email.Folder)
	})

	t.Run("draft email", func(t *testing.T) {
		email := &storage.Email{
			MailboxPubkey: testPubkey,
			FromAddress:   "sender@example.com",
			ToAddress:     "recipient@example.com",
			Subject:       "Draft Subject",
			Body:          "Draft Body",
			Direction:     "draft",
		}

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO emails`)).
			WithArgs(
				sqlmock.AnyArg(), email.MailboxPubkey, nil, email.FromAddress, email.ToAddress,
				nil, nil, email.Subject, email.Body, nil,
				false, nil, nil, nil, nil,
				"draft", "active", "Drafts", pq.Array([]string(nil)), nil, nil,
			).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).
				AddRow(now, now))

		err := postgres.CreateEmail(context.Background(), email)
		assert.NoError(t, err)
		assert.Equal(t, "Drafts", email.Folder)
	})
}

// TestPostgresCreateContact tests contact creation with mock
func TestPostgresCreateContact(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	logger, _ := zap.NewDevelopment()
	postgres := newTestPostgres(db, logger)

	now := time.Now()
	name := "Alice"
	npub := "npub1alice..."

	t.Run("successful contact creation", func(t *testing.T) {
		contact := &storage.Contact{
			MailboxPubkey: testPubkey,
			Email:         "alice@example.com",
			Name:          &name,
			Npub:          &npub,
			AlwaysEncrypt: true,
		}

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO contacts`)).
			WithArgs(
				sqlmock.AnyArg(), contact.MailboxPubkey, contact.Email, contact.Name, contact.Npub,
				nil, nil, nil, contact.AlwaysEncrypt, false,
			).
			WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).
				AddRow(now, now))

		err := postgres.CreateContact(context.Background(), contact)
		assert.NoError(t, err)
		assert.NotEmpty(t, contact.ID)
	})

	t.Run("duplicate contact error", func(t *testing.T) {
		contact := &storage.Contact{
			MailboxPubkey: testPubkey,
			Email:         "alice@example.com",
		}

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO contacts`)).
			WithArgs(
				sqlmock.AnyArg(), contact.MailboxPubkey, contact.Email, nil, nil,
				nil, nil, nil, false, false,
			).
			WillReturnError(&pq.Error{Code: "23505"})

		err := postgres.CreateContact(context.Background(), contact)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "contact already exists")
	})
}

// TestPostgresMarkEmailAsRead tests marking email as read
func TestPostgresMarkEmailAsRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	logger, _ := zap.NewDevelopment()
	postgres := newTestPostgres(db, logger)

	t.Run("mark as read", func(t *testing.T) {
		emailID := "email-uuid"

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE emails SET read_at = CURRENT_TIMESTAMP`)).
			WithArgs(emailID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := postgres.MarkEmailAsRead(context.Background(), emailID)
		assert.NoError(t, err)
	})
}

// TestPostgresMoveEmailToFolder tests moving email to folder
func TestPostgresMoveEmailToFolder(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	logger, _ := zap.NewDevelopment()
	postgres := newTestPostgres(db, logger)

	t.Run("move to folder", func(t *testing.T) {
		emailID := "email-uuid"
		folder := "Archive"

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE emails SET folder = $2`)).
			WithArgs(emailID, folder).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := postgres.MoveEmailToFolder(context.Background(), emailID, folder)
		assert.NoError(t, err)
	})

	t.Run("email not found", func(t *testing.T) {
		emailID := "nonexistent-uuid"
		folder := "Archive"

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE emails SET folder = $2`)).
			WithArgs(emailID, folder).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := postgres.MoveEmailToFolder(context.Background(), emailID, folder)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "email not found")
	})
}

// TestPostgresDeleteEmail tests email soft-delete
func TestPostgresDeleteEmail(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	logger, _ := zap.NewDevelopment()
	postgres := newTestPostgres(db, logger)

	t.Run("delete email", func(t *testing.T) {
		emailID := "email-uuid"

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE emails SET deleted_at = CURRENT_TIMESTAMP`)).
			WithArgs(emailID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := postgres.DeleteEmail(context.Background(), emailID)
		assert.NoError(t, err)
	})

	t.Run("email not found", func(t *testing.T) {
		emailID := "nonexistent-uuid"

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE emails SET deleted_at = CURRENT_TIMESTAMP`)).
			WithArgs(emailID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := postgres.DeleteEmail(context.Background(), emailID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "email not found")
	})
}

// TestPostgresDeleteContact tests contact soft-delete
func TestPostgresDeleteContact(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	logger, _ := zap.NewDevelopment()
	postgres := newTestPostgres(db, logger)

	t.Run("delete contact", func(t *testing.T) {
		contactID := "contact-uuid"

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE contacts SET deleted_at = CURRENT_TIMESTAMP`)).
			WithArgs(contactID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := postgres.DeleteContact(context.Background(), contactID)
		assert.NoError(t, err)
	})

	t.Run("contact not found", func(t *testing.T) {
		contactID := "nonexistent-uuid"

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE contacts SET deleted_at = CURRENT_TIMESTAMP`)).
			WithArgs(contactID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := postgres.DeleteContact(context.Background(), contactID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "contact not found")
	})
}

// TestPostgresCacheNIP05 tests NIP-05 caching
func TestPostgresCacheNIP05(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	logger, _ := zap.NewDevelopment()
	postgres := newTestPostgres(db, logger)

	t.Run("cache valid NIP-05", func(t *testing.T) {
		email := "alice@example.com"
		npub := "npub1alice..."

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO nip05_cache`)).
			WithArgs(sqlmock.AnyArg(), email, &npub, sqlmock.AnyArg(), true).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := postgres.CacheNIP05(context.Background(), email, &npub, 24*time.Hour)
		assert.NoError(t, err)
	})

	t.Run("cache invalid NIP-05", func(t *testing.T) {
		email := "bob@example.com"

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO nip05_cache`)).
			WithArgs(sqlmock.AnyArg(), email, nil, sqlmock.AnyArg(), false).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := postgres.CacheNIP05(context.Background(), email, nil, 24*time.Hour)
		assert.NoError(t, err)
	})
}

// ============================================================================
// Helper Functions
// ============================================================================

// newTestPostgres creates a PostgreSQL instance with a mock database
// This is a test helper that creates the struct directly
func newTestPostgres(db *sql.DB, logger *zap.Logger) *storage.PostgreSQL {
	// We need to expose the db field or create a test constructor
	// For now, create a wrapper that uses the mock
	return storage.NewPostgresForTesting(db, logger)
}
