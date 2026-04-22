package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"go.uber.org/zap"
)

// PostgreSQL database wrapper
type PostgreSQL struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewPostgres creates a new PostgreSQL database connection
func NewPostgres(databaseURL string, logger *zap.Logger) (*PostgreSQL, error) {
	logger.Info("Initializing PostgreSQL", zap.String("url", maskDatabaseURL(databaseURL)))

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	logger.Info("PostgreSQL connection established")

	return &PostgreSQL{
		db:     db,
		logger: logger,
	}, nil
}

// maskDatabaseURL hides credentials in the database URL for logging
func maskDatabaseURL(url string) string {
	// Simple masking - replace password portion
	if idx := strings.Index(url, "://"); idx > 0 {
		rest := url[idx+3:]
		if atIdx := strings.Index(rest, "@"); atIdx > 0 {
			return url[:idx+3] + "***:***@" + rest[atIdx+1:]
		}
	}
	return url
}

// Ping verifies database connectivity
func (db *PostgreSQL) Ping(ctx context.Context) error {
	return db.db.PingContext(ctx)
}

// Close closes the database connection
func (db *PostgreSQL) Close() error {
	db.logger.Info("Closing database connection")
	return db.db.Close()
}

// DB returns the underlying sql.DB for advanced operations
func (db *PostgreSQL) DB() *sql.DB {
	return db.db
}

// NewPostgresForTesting creates a PostgreSQL instance with a provided db connection
// This is intended for testing with mock databases
func NewPostgresForTesting(db *sql.DB, logger *zap.Logger) *PostgreSQL {
	return &PostgreSQL{
		db:     db,
		logger: logger,
	}
}

// ============================================================================
// Models
// ============================================================================

// User represents a user account with Nostr identity
type User struct {
	ID               string
	Npub             string
	Email            string
	EmailVerified    bool
	EmailVerifiedAt  *time.Time
	PublicKey        string
	EncryptionMethod string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        *time.Time
}

// Email represents an email message
type Email struct {
	ID                string
	UserID            string
	MessageID         *string
	FromAddress       string
	ToAddress         string
	CC                *string
	BCC               *string
	Subject           string
	Body              string
	HTMLBody          *string
	IsEncrypted       bool
	EncryptionNonce   *string
	SenderNpub        *string
	RecipientNpub     *string
	Direction         string // sent, received, draft
	Status            string // active, deleted, archived, spam
	ReadAt            *time.Time
	StalwartMessageID *string
	Folder            string
	Labels            []string

	// Nostr signature verification (RFC-002)
	NostrVerified          bool
	NostrVerificationError *string
	NostrVerifiedAt        *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// Contact represents an address book entry
type Contact struct {
	ID            string
	UserID        string
	Email         string
	Name          *string
	Npub          *string
	Notes         *string
	Organization  *string
	Phone         *string
	AlwaysEncrypt bool
	Blocked       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
}

// Attachment represents an email attachment
type Attachment struct {
	ID            string
	EmailID       string
	Filename      string
	ContentType   *string
	SizeBytes     *int64
	BlossomSHA256 *string
	BlossomURL    *string
	CreatedAt     time.Time
}

// NIP05CacheEntry represents a cached NIP-05 lookup
type NIP05CacheEntry struct {
	ID        string
	Email     string
	Npub      *string
	CachedAt  time.Time
	ExpiresAt *time.Time
	Valid     bool
}

// AuditLogEntry represents an audit log record
type AuditLogEntry struct {
	ID           string
	UserID       *string
	Action       string
	ResourceType *string
	ResourceID   *string
	Details      map[string]interface{}
	IPAddress    *string
	UserAgent    *string
	CreatedAt    time.Time
}

// ListOptions provides pagination and filtering options
type ListOptions struct {
	Limit     int
	Offset    int
	OrderBy   string
	OrderDesc bool
}

// DefaultListOptions returns sensible default options
func DefaultListOptions() ListOptions {
	return ListOptions{
		Limit:     50,
		Offset:    0,
		OrderBy:   "created_at",
		OrderDesc: true,
	}
}

// EmailFilter provides filtering options for email queries
type EmailFilter struct {
	Direction string   // sent, received, draft
	Status    string   // active, deleted, archived, spam
	Folder    string   // INBOX, Sent, Drafts, etc.
	Labels    []string // Custom labels
	Unread    *bool    // Filter by read status
	Search    string   // Search in subject/body
}

// ============================================================================
// User Operations
// ============================================================================

// CreateUser creates a new user
func (db *PostgreSQL) CreateUser(ctx context.Context, user *User) error {
	db.logger.Debug("Creating user", zap.String("npub", user.Npub), zap.String("email", user.Email))

	if user.ID == "" {
		user.ID = uuid.New().String()
	}

	query := `
		INSERT INTO users (id, npub, email, email_verified, public_key, encryption_method)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at
	`

	encMethod := user.EncryptionMethod
	if encMethod == "" {
		encMethod = "nip44"
	}

	err := db.db.QueryRowContext(ctx, query,
		user.ID, user.Npub, user.Email, user.EmailVerified, user.PublicKey, encMethod,
	).Scan(&user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("user already exists with npub or email: %w", err)
		}
		return fmt.Errorf("failed to create user: %w", err)
	}

	db.logger.Info("User created", zap.String("id", user.ID), zap.String("email", user.Email))
	return nil
}

// GetUser retrieves a user by ID
func (db *PostgreSQL) GetUser(ctx context.Context, id string) (*User, error) {
	db.logger.Debug("Getting user by ID", zap.String("id", id))

	query := `
		SELECT id, npub, email, email_verified, email_verified_at, public_key,
		       encryption_method, created_at, updated_at, deleted_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`

	user := &User{}
	err := db.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID, &user.Npub, &user.Email, &user.EmailVerified, &user.EmailVerifiedAt,
		&user.PublicKey, &user.EncryptionMethod, &user.CreatedAt, &user.UpdatedAt, &user.DeletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return user, nil
}

// GetUserByNpub retrieves a user by Nostr public key
func (db *PostgreSQL) GetUserByNpub(ctx context.Context, npub string) (*User, error) {
	db.logger.Debug("Getting user by npub", zap.String("npub", npub))

	query := `
		SELECT id, npub, email, email_verified, email_verified_at, public_key,
		       encryption_method, created_at, updated_at, deleted_at
		FROM users
		WHERE npub = $1 AND deleted_at IS NULL
	`

	user := &User{}
	err := db.db.QueryRowContext(ctx, query, npub).Scan(
		&user.ID, &user.Npub, &user.Email, &user.EmailVerified, &user.EmailVerifiedAt,
		&user.PublicKey, &user.EncryptionMethod, &user.CreatedAt, &user.UpdatedAt, &user.DeletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user by npub: %w", err)
	}

	return user, nil
}

// GetUserByEmail retrieves a user by email address
func (db *PostgreSQL) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	db.logger.Debug("Getting user by email", zap.String("email", email))

	query := `
		SELECT id, npub, email, email_verified, email_verified_at, public_key,
		       encryption_method, created_at, updated_at, deleted_at
		FROM users
		WHERE email = $1 AND deleted_at IS NULL
	`

	user := &User{}
	err := db.db.QueryRowContext(ctx, query, email).Scan(
		&user.ID, &user.Npub, &user.Email, &user.EmailVerified, &user.EmailVerifiedAt,
		&user.PublicKey, &user.EncryptionMethod, &user.CreatedAt, &user.UpdatedAt, &user.DeletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user by email: %w", err)
	}

	return user, nil
}

// UpdateUser updates a user's information
func (db *PostgreSQL) UpdateUser(ctx context.Context, user *User) error {
	db.logger.Debug("Updating user", zap.String("id", user.ID))

	query := `
		UPDATE users
		SET email = $2, email_verified = $3, email_verified_at = $4,
		    public_key = $5, encryption_method = $6
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING updated_at
	`

	err := db.db.QueryRowContext(ctx, query,
		user.ID, user.Email, user.EmailVerified, user.EmailVerifiedAt,
		user.PublicKey, user.EncryptionMethod,
	).Scan(&user.UpdatedAt)

	if err == sql.ErrNoRows {
		return fmt.Errorf("user not found: %s", user.ID)
	}
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	return nil
}

// DeleteUser soft-deletes a user
func (db *PostgreSQL) DeleteUser(ctx context.Context, id string) error {
	db.logger.Debug("Deleting user", zap.String("id", id))

	query := `UPDATE users SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1 AND deleted_at IS NULL`

	result, err := db.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("user not found: %s", id)
	}

	db.logger.Info("User deleted", zap.String("id", id))
	return nil
}

// ============================================================================
// Email Operations
// ============================================================================

// CreateEmail creates a new email
func (db *PostgreSQL) CreateEmail(ctx context.Context, email *Email) error {
	db.logger.Debug("Creating email",
		zap.String("from", email.FromAddress),
		zap.String("to", email.ToAddress),
		zap.String("direction", email.Direction))

	if email.ID == "" {
		email.ID = uuid.New().String()
	}
	if email.Direction == "" {
		email.Direction = "sent"
	}
	if email.Status == "" {
		email.Status = "active"
	}
	if email.Folder == "" {
		if email.Direction == "sent" {
			email.Folder = "Sent"
		} else if email.Direction == "draft" {
			email.Folder = "Drafts"
		} else {
			email.Folder = "INBOX"
		}
	}

	query := `
		INSERT INTO emails (
			id, user_id, message_id, from_address, to_address, cc, bcc,
			subject, body, html_body, is_encrypted, encryption_nonce,
			sender_npub, recipient_npub, direction, status, folder, labels
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		RETURNING created_at, updated_at
	`

	err := db.db.QueryRowContext(ctx, query,
		email.ID, email.UserID, email.MessageID, email.FromAddress, email.ToAddress,
		email.CC, email.BCC, email.Subject, email.Body, email.HTMLBody,
		email.IsEncrypted, email.EncryptionNonce, email.SenderNpub, email.RecipientNpub,
		email.Direction, email.Status, email.Folder, pq.Array(email.Labels),
	).Scan(&email.CreatedAt, &email.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create email: %w", err)
	}

	db.logger.Info("Email created", zap.String("id", email.ID))
	return nil
}

// GetEmail retrieves an email by ID
func (db *PostgreSQL) GetEmail(ctx context.Context, id string) (*Email, error) {
	db.logger.Debug("Getting email", zap.String("id", id))

	query := `
		SELECT id, user_id, message_id, from_address, to_address, cc, bcc,
		       subject, body, html_body, is_encrypted, encryption_nonce,
		       sender_npub, recipient_npub, direction, status, read_at,
		       stalwart_message_id, folder, labels, created_at, updated_at, deleted_at
		FROM emails
		WHERE id = $1 AND deleted_at IS NULL
	`

	email := &Email{}
	var labels pq.StringArray
	err := db.db.QueryRowContext(ctx, query, id).Scan(
		&email.ID, &email.UserID, &email.MessageID, &email.FromAddress, &email.ToAddress,
		&email.CC, &email.BCC, &email.Subject, &email.Body, &email.HTMLBody,
		&email.IsEncrypted, &email.EncryptionNonce, &email.SenderNpub, &email.RecipientNpub,
		&email.Direction, &email.Status, &email.ReadAt, &email.StalwartMessageID,
		&email.Folder, &labels, &email.CreatedAt, &email.UpdatedAt, &email.DeletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get email: %w", err)
	}

	email.Labels = labels
	return email, nil
}

// GetEmailByMessageID retrieves an email by its Message-ID header
func (db *PostgreSQL) GetEmailByMessageID(ctx context.Context, userID, messageID string) (*Email, error) {
	db.logger.Debug("Getting email by message ID", zap.String("message_id", messageID))

	query := `
		SELECT id, user_id, message_id, from_address, to_address, cc, bcc,
		       subject, body, html_body, is_encrypted, encryption_nonce,
		       sender_npub, recipient_npub, direction, status, read_at,
		       stalwart_message_id, folder, labels, created_at, updated_at, deleted_at
		FROM emails
		WHERE user_id = $1 AND message_id = $2 AND deleted_at IS NULL
	`

	email := &Email{}
	var labels pq.StringArray
	err := db.db.QueryRowContext(ctx, query, userID, messageID).Scan(
		&email.ID, &email.UserID, &email.MessageID, &email.FromAddress, &email.ToAddress,
		&email.CC, &email.BCC, &email.Subject, &email.Body, &email.HTMLBody,
		&email.IsEncrypted, &email.EncryptionNonce, &email.SenderNpub, &email.RecipientNpub,
		&email.Direction, &email.Status, &email.ReadAt, &email.StalwartMessageID,
		&email.Folder, &labels, &email.CreatedAt, &email.UpdatedAt, &email.DeletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get email by message ID: %w", err)
	}

	email.Labels = labels
	return email, nil
}

// ListEmails lists emails for a user with filtering and pagination
func (db *PostgreSQL) ListEmails(ctx context.Context, userID string, filter *EmailFilter, opts ListOptions) ([]*Email, int, error) {
	db.logger.Debug("Listing emails",
		zap.String("user_id", userID),
		zap.Int("limit", opts.Limit),
		zap.Int("offset", opts.Offset))

	// Build the query
	baseQuery := `
		FROM emails
		WHERE user_id = $1 AND deleted_at IS NULL
	`
	args := []interface{}{userID}
	argCount := 1

	// Apply filters
	if filter != nil {
		if filter.Direction != "" {
			argCount++
			baseQuery += fmt.Sprintf(" AND direction = $%d", argCount)
			args = append(args, filter.Direction)
		}
		if filter.Status != "" {
			argCount++
			baseQuery += fmt.Sprintf(" AND status = $%d", argCount)
			args = append(args, filter.Status)
		}
		if filter.Folder != "" {
			argCount++
			baseQuery += fmt.Sprintf(" AND folder = $%d", argCount)
			args = append(args, filter.Folder)
		}
		if filter.Unread != nil {
			if *filter.Unread {
				baseQuery += " AND read_at IS NULL"
			} else {
				baseQuery += " AND read_at IS NOT NULL"
			}
		}
		if filter.Search != "" {
			argCount++
			searchArg := argCount
			baseQuery += fmt.Sprintf(" AND (subject ILIKE $%d OR body ILIKE $%d)", searchArg, searchArg)
			args = append(args, "%"+filter.Search+"%")
		}
		if len(filter.Labels) > 0 {
			argCount++
			baseQuery += fmt.Sprintf(" AND labels && $%d", argCount)
			args = append(args, pq.Array(filter.Labels))
		}
	}

	// Count total
	var total int
	countQuery := "SELECT COUNT(*) " + baseQuery
	if err := db.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count emails: %w", err)
	}

	// Get paginated results
	orderDir := "DESC"
	if !opts.OrderDesc {
		orderDir = "ASC"
	}
	orderBy := opts.OrderBy
	if orderBy == "" {
		orderBy = "created_at"
	}

	selectQuery := fmt.Sprintf(`
		SELECT id, user_id, message_id, from_address, to_address, cc, bcc,
		       subject, body, html_body, is_encrypted, encryption_nonce,
		       sender_npub, recipient_npub, direction, status, read_at,
		       stalwart_message_id, folder, labels, created_at, updated_at, deleted_at
		%s
		ORDER BY %s %s
		LIMIT $%d OFFSET $%d
	`, baseQuery, orderBy, orderDir, argCount+1, argCount+2)

	args = append(args, opts.Limit, opts.Offset)

	rows, err := db.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list emails: %w", err)
	}
	defer rows.Close()

	var emails []*Email
	for rows.Next() {
		email := &Email{}
		var labels pq.StringArray
		err := rows.Scan(
			&email.ID, &email.UserID, &email.MessageID, &email.FromAddress, &email.ToAddress,
			&email.CC, &email.BCC, &email.Subject, &email.Body, &email.HTMLBody,
			&email.IsEncrypted, &email.EncryptionNonce, &email.SenderNpub, &email.RecipientNpub,
			&email.Direction, &email.Status, &email.ReadAt, &email.StalwartMessageID,
			&email.Folder, &labels, &email.CreatedAt, &email.UpdatedAt, &email.DeletedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan email: %w", err)
		}
		email.Labels = labels
		emails = append(emails, email)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating emails: %w", err)
	}

	return emails, total, nil
}

// UpdateEmail updates an email
func (db *PostgreSQL) UpdateEmail(ctx context.Context, email *Email) error {
	db.logger.Debug("Updating email", zap.String("id", email.ID))

	query := `
		UPDATE emails
		SET subject = $2, body = $3, html_body = $4, status = $5, folder = $6, labels = $7
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING updated_at
	`

	err := db.db.QueryRowContext(ctx, query,
		email.ID, email.Subject, email.Body, email.HTMLBody, email.Status, email.Folder, pq.Array(email.Labels),
	).Scan(&email.UpdatedAt)

	if err == sql.ErrNoRows {
		return fmt.Errorf("email not found: %s", email.ID)
	}
	if err != nil {
		return fmt.Errorf("failed to update email: %w", err)
	}

	return nil
}

// MarkEmailAsRead marks an email as read
func (db *PostgreSQL) MarkEmailAsRead(ctx context.Context, id string) error {
	db.logger.Debug("Marking email as read", zap.String("id", id))

	query := `UPDATE emails SET read_at = CURRENT_TIMESTAMP WHERE id = $1 AND read_at IS NULL AND deleted_at IS NULL`

	_, err := db.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to mark email as read: %w", err)
	}

	return nil
}

// MoveEmailToFolder moves an email to a different folder
func (db *PostgreSQL) MoveEmailToFolder(ctx context.Context, id, folder string) error {
	db.logger.Debug("Moving email to folder", zap.String("id", id), zap.String("folder", folder))

	query := `UPDATE emails SET folder = $2 WHERE id = $1 AND deleted_at IS NULL`

	result, err := db.db.ExecContext(ctx, query, id, folder)
	if err != nil {
		return fmt.Errorf("failed to move email: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("email not found: %s", id)
	}

	return nil
}

// DeleteEmail soft-deletes an email
func (db *PostgreSQL) DeleteEmail(ctx context.Context, id string) error {
	db.logger.Debug("Deleting email", zap.String("id", id))

	query := `UPDATE emails SET deleted_at = CURRENT_TIMESTAMP, status = 'deleted' WHERE id = $1 AND deleted_at IS NULL`

	result, err := db.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete email: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("email not found: %s", id)
	}

	return nil
}

// PermanentlyDeleteEmail permanently removes an email
func (db *PostgreSQL) PermanentlyDeleteEmail(ctx context.Context, id string) error {
	db.logger.Debug("Permanently deleting email", zap.String("id", id))

	// Use transaction to ensure atomicity
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// First delete attachments
	_, err = tx.ExecContext(ctx, `DELETE FROM attachments WHERE email_id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete attachments: %w", err)
	}

	// Then delete the email
	_, err = tx.ExecContext(ctx, `DELETE FROM emails WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to permanently delete email: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetEmailStats returns email statistics for a user
func (db *PostgreSQL) GetEmailStats(ctx context.Context, userID string) (map[string]int, error) {
	db.logger.Debug("Getting email stats", zap.String("user_id", userID))

	query := `
		SELECT
			COUNT(*) FILTER (WHERE status = 'active') as total,
			COUNT(*) FILTER (WHERE read_at IS NULL AND direction = 'received' AND status = 'active') as unread,
			COUNT(*) FILTER (WHERE direction = 'sent' AND status = 'active') as sent,
			COUNT(*) FILTER (WHERE direction = 'received' AND status = 'active') as received,
			COUNT(*) FILTER (WHERE direction = 'draft') as drafts,
			COUNT(*) FILTER (WHERE status = 'spam') as spam,
			COUNT(*) FILTER (WHERE status = 'deleted') as trash
		FROM emails
		WHERE user_id = $1
	`

	var total, unread, sent, received, drafts, spam, trash int
	err := db.db.QueryRowContext(ctx, query, userID).Scan(
		&total, &unread, &sent, &received, &drafts, &spam, &trash,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get email stats: %w", err)
	}

	return map[string]int{
		"total":    total,
		"unread":   unread,
		"sent":     sent,
		"received": received,
		"drafts":   drafts,
		"spam":     spam,
		"trash":    trash,
	}, nil
}

// ============================================================================
// Contact Operations
// ============================================================================

// CreateContact creates a new contact
func (db *PostgreSQL) CreateContact(ctx context.Context, contact *Contact) error {
	db.logger.Debug("Creating contact",
		zap.String("user_id", contact.UserID),
		zap.String("email", contact.Email))

	if contact.ID == "" {
		contact.ID = uuid.New().String()
	}

	query := `
		INSERT INTO contacts (id, user_id, email, name, npub, notes, organization, phone, always_encrypt, blocked)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING created_at, updated_at
	`

	err := db.db.QueryRowContext(ctx, query,
		contact.ID, contact.UserID, contact.Email, contact.Name, contact.Npub,
		contact.Notes, contact.Organization, contact.Phone, contact.AlwaysEncrypt, contact.Blocked,
	).Scan(&contact.CreatedAt, &contact.UpdatedAt)

	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("contact already exists for this email: %w", err)
		}
		return fmt.Errorf("failed to create contact: %w", err)
	}

	db.logger.Info("Contact created", zap.String("id", contact.ID))
	return nil
}

// GetContact retrieves a contact by ID
func (db *PostgreSQL) GetContact(ctx context.Context, id string) (*Contact, error) {
	db.logger.Debug("Getting contact", zap.String("id", id))

	query := `
		SELECT id, user_id, email, name, npub, notes, organization, phone,
		       always_encrypt, blocked, created_at, updated_at, deleted_at
		FROM contacts
		WHERE id = $1 AND deleted_at IS NULL
	`

	contact := &Contact{}
	err := db.db.QueryRowContext(ctx, query, id).Scan(
		&contact.ID, &contact.UserID, &contact.Email, &contact.Name, &contact.Npub,
		&contact.Notes, &contact.Organization, &contact.Phone,
		&contact.AlwaysEncrypt, &contact.Blocked, &contact.CreatedAt, &contact.UpdatedAt, &contact.DeletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get contact: %w", err)
	}

	return contact, nil
}

// GetContactByEmail retrieves a contact by email for a user
func (db *PostgreSQL) GetContactByEmail(ctx context.Context, userID, email string) (*Contact, error) {
	db.logger.Debug("Getting contact by email", zap.String("user_id", userID), zap.String("email", email))

	query := `
		SELECT id, user_id, email, name, npub, notes, organization, phone,
		       always_encrypt, blocked, created_at, updated_at, deleted_at
		FROM contacts
		WHERE user_id = $1 AND email = $2 AND deleted_at IS NULL
	`

	contact := &Contact{}
	err := db.db.QueryRowContext(ctx, query, userID, email).Scan(
		&contact.ID, &contact.UserID, &contact.Email, &contact.Name, &contact.Npub,
		&contact.Notes, &contact.Organization, &contact.Phone,
		&contact.AlwaysEncrypt, &contact.Blocked, &contact.CreatedAt, &contact.UpdatedAt, &contact.DeletedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get contact by email: %w", err)
	}

	return contact, nil
}

// ListContacts lists contacts for a user
func (db *PostgreSQL) ListContacts(ctx context.Context, userID string, opts ListOptions) ([]*Contact, int, error) {
	db.logger.Debug("Listing contacts",
		zap.String("user_id", userID),
		zap.Int("limit", opts.Limit),
		zap.Int("offset", opts.Offset))

	// Count total
	var total int
	countQuery := `SELECT COUNT(*) FROM contacts WHERE user_id = $1 AND deleted_at IS NULL`
	if err := db.db.QueryRowContext(ctx, countQuery, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count contacts: %w", err)
	}

	// Get paginated results
	orderDir := "ASC"
	if opts.OrderDesc {
		orderDir = "DESC"
	}
	orderBy := opts.OrderBy
	if orderBy == "" {
		orderBy = "name"
	}

	selectQuery := fmt.Sprintf(`
		SELECT id, user_id, email, name, npub, notes, organization, phone,
		       always_encrypt, blocked, created_at, updated_at, deleted_at
		FROM contacts
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY %s %s NULLS LAST
		LIMIT $2 OFFSET $3
	`, orderBy, orderDir)

	rows, err := db.db.QueryContext(ctx, selectQuery, userID, opts.Limit, opts.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list contacts: %w", err)
	}
	defer rows.Close()

	var contacts []*Contact
	for rows.Next() {
		contact := &Contact{}
		err := rows.Scan(
			&contact.ID, &contact.UserID, &contact.Email, &contact.Name, &contact.Npub,
			&contact.Notes, &contact.Organization, &contact.Phone,
			&contact.AlwaysEncrypt, &contact.Blocked, &contact.CreatedAt, &contact.UpdatedAt, &contact.DeletedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan contact: %w", err)
		}
		contacts = append(contacts, contact)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating contacts: %w", err)
	}

	return contacts, total, nil
}

// SearchContacts searches contacts by name or email
func (db *PostgreSQL) SearchContacts(ctx context.Context, userID, query string, limit int) ([]*Contact, error) {
	db.logger.Debug("Searching contacts", zap.String("user_id", userID), zap.String("query", query))

	if limit <= 0 {
		limit = 10
	}

	sqlQuery := `
		SELECT id, user_id, email, name, npub, notes, organization, phone,
		       always_encrypt, blocked, created_at, updated_at, deleted_at
		FROM contacts
		WHERE user_id = $1 AND deleted_at IS NULL
		  AND (name ILIKE $2 OR email ILIKE $2)
		ORDER BY name ASC NULLS LAST
		LIMIT $3
	`

	rows, err := db.db.QueryContext(ctx, sqlQuery, userID, "%"+query+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search contacts: %w", err)
	}
	defer rows.Close()

	var contacts []*Contact
	for rows.Next() {
		contact := &Contact{}
		err := rows.Scan(
			&contact.ID, &contact.UserID, &contact.Email, &contact.Name, &contact.Npub,
			&contact.Notes, &contact.Organization, &contact.Phone,
			&contact.AlwaysEncrypt, &contact.Blocked, &contact.CreatedAt, &contact.UpdatedAt, &contact.DeletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan contact: %w", err)
		}
		contacts = append(contacts, contact)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating contacts: %w", err)
	}

	return contacts, nil
}

// UpdateContact updates a contact
func (db *PostgreSQL) UpdateContact(ctx context.Context, contact *Contact) error {
	db.logger.Debug("Updating contact", zap.String("id", contact.ID))

	query := `
		UPDATE contacts
		SET email = $2, name = $3, npub = $4, notes = $5, organization = $6,
		    phone = $7, always_encrypt = $8, blocked = $9
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING updated_at
	`

	err := db.db.QueryRowContext(ctx, query,
		contact.ID, contact.Email, contact.Name, contact.Npub, contact.Notes,
		contact.Organization, contact.Phone, contact.AlwaysEncrypt, contact.Blocked,
	).Scan(&contact.UpdatedAt)

	if err == sql.ErrNoRows {
		return fmt.Errorf("contact not found: %s", contact.ID)
	}
	if err != nil {
		return fmt.Errorf("failed to update contact: %w", err)
	}

	return nil
}

// DeleteContact soft-deletes a contact
func (db *PostgreSQL) DeleteContact(ctx context.Context, id string) error {
	db.logger.Debug("Deleting contact", zap.String("id", id))

	query := `UPDATE contacts SET deleted_at = CURRENT_TIMESTAMP WHERE id = $1 AND deleted_at IS NULL`

	result, err := db.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete contact: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("contact not found: %s", id)
	}

	db.logger.Info("Contact deleted", zap.String("id", id))
	return nil
}

// ============================================================================
// Attachment Operations
// ============================================================================

// CreateAttachment creates a new attachment
func (db *PostgreSQL) CreateAttachment(ctx context.Context, attachment *Attachment) error {
	db.logger.Debug("Creating attachment",
		zap.String("email_id", attachment.EmailID),
		zap.String("filename", attachment.Filename))

	if attachment.ID == "" {
		attachment.ID = uuid.New().String()
	}

	query := `
		INSERT INTO attachments (id, email_id, filename, content_type, size_bytes, blossom_sha256, blossom_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at
	`

	err := db.db.QueryRowContext(ctx, query,
		attachment.ID, attachment.EmailID, attachment.Filename, attachment.ContentType,
		attachment.SizeBytes, attachment.BlossomSHA256, attachment.BlossomURL,
	).Scan(&attachment.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to create attachment: %w", err)
	}

	return nil
}

// GetAttachmentsByEmail retrieves all attachments for an email
func (db *PostgreSQL) GetAttachmentsByEmail(ctx context.Context, emailID string) ([]*Attachment, error) {
	db.logger.Debug("Getting attachments for email", zap.String("email_id", emailID))

	query := `
		SELECT id, email_id, filename, content_type, size_bytes, blossom_sha256, blossom_url, created_at
		FROM attachments
		WHERE email_id = $1
		ORDER BY created_at
	`

	rows, err := db.db.QueryContext(ctx, query, emailID)
	if err != nil {
		return nil, fmt.Errorf("failed to get attachments: %w", err)
	}
	defer rows.Close()

	var attachments []*Attachment
	for rows.Next() {
		att := &Attachment{}
		err := rows.Scan(
			&att.ID, &att.EmailID, &att.Filename, &att.ContentType,
			&att.SizeBytes, &att.BlossomSHA256, &att.BlossomURL, &att.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan attachment: %w", err)
		}
		attachments = append(attachments, att)
	}

	return attachments, nil
}

// ============================================================================
// NIP-05 Cache Operations
// ============================================================================

// CacheNIP05 caches a NIP-05 lookup result
func (db *PostgreSQL) CacheNIP05(ctx context.Context, email string, npub *string, ttl time.Duration) error {
	db.logger.Debug("Caching NIP-05 lookup", zap.String("email", email))

	expiresAt := time.Now().Add(ttl)
	valid := npub != nil

	query := `
		INSERT INTO nip05_cache (id, email, npub, expires_at, valid)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (email) DO UPDATE
		SET npub = $3, cached_at = CURRENT_TIMESTAMP, expires_at = $4, valid = $5
	`

	_, err := db.db.ExecContext(ctx, query, uuid.New().String(), email, npub, expiresAt, valid)
	if err != nil {
		return fmt.Errorf("failed to cache NIP-05: %w", err)
	}

	return nil
}

// GetCachedNIP05 retrieves a cached NIP-05 lookup
func (db *PostgreSQL) GetCachedNIP05(ctx context.Context, email string) (*NIP05CacheEntry, error) {
	db.logger.Debug("Getting cached NIP-05", zap.String("email", email))

	query := `
		SELECT id, email, npub, cached_at, expires_at, valid
		FROM nip05_cache
		WHERE email = $1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
	`

	entry := &NIP05CacheEntry{}
	err := db.db.QueryRowContext(ctx, query, email).Scan(
		&entry.ID, &entry.Email, &entry.Npub, &entry.CachedAt, &entry.ExpiresAt, &entry.Valid,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get cached NIP-05: %w", err)
	}

	return entry, nil
}

// ============================================================================
// Audit Log Operations
// ============================================================================

// LogAuditEvent logs an audit event
func (db *PostgreSQL) LogAuditEvent(ctx context.Context, entry *AuditLogEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}

	query := `
		INSERT INTO audit_log (id, user_id, action, resource_type, resource_id, details, ip_address, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	var detailsJSON []byte
	if entry.Details != nil {
		var err error
		detailsJSON, err = encodeJSON(entry.Details)
		if err != nil {
			return fmt.Errorf("failed to encode audit details: %w", err)
		}
	}

	_, err := db.db.ExecContext(ctx, query,
		entry.ID, entry.UserID, entry.Action, entry.ResourceType, entry.ResourceID,
		detailsJSON, entry.IPAddress, entry.UserAgent,
	)
	if err != nil {
		return fmt.Errorf("failed to log audit event: %w", err)
	}

	return nil
}

// ============================================================================
// Migrations
// ============================================================================

// Migrate runs database migrations
func (db *PostgreSQL) Migrate(ctx context.Context) error {
	db.logger.Info("Running database migrations")

	// Read and execute the schema file
	// In production, you'd use a migration tool like golang-migrate
	// For now, we'll just check if tables exist

	var exists bool
	err := db.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_name = 'users'
		)
	`).Scan(&exists)

	if err != nil {
		return fmt.Errorf("failed to check migration status: %w", err)
	}

	if !exists {
		db.logger.Warn("Database tables not found - please run schema.sql manually")
		return fmt.Errorf("database not initialized: please run configs/schema.sql")
	}

	db.logger.Info("Database migrations complete")
	return nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// isUniqueViolation checks if an error is a PostgreSQL unique constraint violation
func isUniqueViolation(err error) bool {
	if pqErr, ok := err.(*pq.Error); ok {
		return pqErr.Code == "23505" // unique_violation
	}
	return false
}

// encodeJSON encodes a value to JSON bytes
func encodeJSON(v interface{}) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// ============================================================================
// Address Operations (uses cloistr-me's 'addresses' table)
// ============================================================================
//
// NOTE: Address mappings (npub <-> email) are owned by cloistr-me.
// cloistr-email queries the shared 'addresses' table directly.
// Both services use the same PostgreSQL database.
//
// cloistr-me schema:
//   addresses(id, username, domain, pubkey, active, verified,
//             created_at, updated_at, expires_at, grace_period_ends, ban_reason)

// Address represents an address record from cloistr-me's addresses table
type Address struct {
	ID              int64
	Username        string // local part (alice)
	Domain          string // domain (cloistr.xyz)
	Pubkey          string // hex npub
	Active          bool
	Verified        bool
	DisplayName     *string // nullable, added via migration
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExpiresAt       *time.Time
	GracePeriodEnds *time.Time
	BanReason       *string
}

// Email returns the full email address (username@domain)
func (a *Address) Email() string {
	return a.Username + "@" + a.Domain
}

// GetAddressByPubkey retrieves an address by pubkey (npub)
func (db *PostgreSQL) GetAddressByPubkey(ctx context.Context, pubkey string) (*Address, error) {
	db.logger.Debug("Getting address by pubkey", zap.String("pubkey", pubkey))

	query := `
		SELECT id, username, domain, pubkey, active, verified, display_name,
		       created_at, updated_at, expires_at, grace_period_ends, ban_reason
		FROM addresses
		WHERE pubkey = $1 AND active = true
	`

	addr := &Address{}
	err := db.db.QueryRowContext(ctx, query, pubkey).Scan(
		&addr.ID, &addr.Username, &addr.Domain, &addr.Pubkey,
		&addr.Active, &addr.Verified, &addr.DisplayName,
		&addr.CreatedAt, &addr.UpdatedAt, &addr.ExpiresAt,
		&addr.GracePeriodEnds, &addr.BanReason,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get address by pubkey: %w", err)
	}

	return addr, nil
}

// GetAddressByEmail retrieves an address by email (username@domain)
func (db *PostgreSQL) GetAddressByEmail(ctx context.Context, email string) (*Address, error) {
	db.logger.Debug("Getting address by email", zap.String("email", email))

	// Parse email into username and domain
	parts := strings.SplitN(strings.ToLower(email), "@", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid email format: %s", email)
	}
	username, domain := parts[0], parts[1]

	query := `
		SELECT id, username, domain, pubkey, active, verified, display_name,
		       created_at, updated_at, expires_at, grace_period_ends, ban_reason
		FROM addresses
		WHERE username = $1 AND domain = $2 AND active = true
	`

	addr := &Address{}
	err := db.db.QueryRowContext(ctx, query, username, domain).Scan(
		&addr.ID, &addr.Username, &addr.Domain, &addr.Pubkey,
		&addr.Active, &addr.Verified, &addr.DisplayName,
		&addr.CreatedAt, &addr.UpdatedAt, &addr.ExpiresAt,
		&addr.GracePeriodEnds, &addr.BanReason,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get address by email: %w", err)
	}

	return addr, nil
}

// UsernameExists checks if a username is already taken for a given domain
func (db *PostgreSQL) UsernameExists(ctx context.Context, username, domain string) (bool, error) {
	db.logger.Debug("Checking username existence",
		zap.String("username", username),
		zap.String("domain", domain))

	query := `SELECT EXISTS(SELECT 1 FROM addresses WHERE username = $1 AND domain = $2)`

	var exists bool
	err := db.db.QueryRowContext(ctx, query, strings.ToLower(username), strings.ToLower(domain)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check username existence: %w", err)
	}

	return exists, nil
}
