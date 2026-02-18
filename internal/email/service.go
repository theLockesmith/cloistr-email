// Package email provides the core email service that coordinates
// identity validation, encryption, storage, and transport.
package email

import (
	"context"
	"fmt"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/encryption"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/identity"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/metrics"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/transport"
	"go.uber.org/zap"
)

// SendRequest contains everything needed to send an email
type SendRequest struct {
	// SenderNpub is the sender's Nostr public key (hex)
	SenderNpub string

	// Recipients
	To  []string
	CC  []string
	BCC []string

	// Content
	Subject  string
	Body     string
	HTMLBody string

	// Encryption
	EncryptionMode   encryption.EncryptionMode
	PreEncryptedBody string // For NIP-07 client-side encryption
	RecipientPubkeys map[string]string

	// Threading
	InReplyTo  string
	References []string

	// Transport preference (optional)
	PreferredTransport transport.TransportType
}

// SendResult contains the result of a send operation
type SendResult struct {
	Success   bool
	MessageID string
	EmailID   string // Database ID

	// Per-recipient results
	Recipients []RecipientSendResult

	Error string
}

// RecipientSendResult contains status for a single recipient
type RecipientSendResult struct {
	Email     string
	Success   bool
	Encrypted bool
	Error     string
}

// Service coordinates email operations
type Service struct {
	identitySvc      *identity.Service
	transportMgr     *transport.Manager
	encryptionSvc    *encryption.EncryptionService
	db               *storage.PostgreSQL
	logger           *zap.Logger
}

// NewService creates a new email service
func NewService(
	identitySvc *identity.Service,
	transportMgr *transport.Manager,
	encryptionSvc *encryption.EncryptionService,
	db *storage.PostgreSQL,
	logger *zap.Logger,
) *Service {
	return &Service{
		identitySvc:   identitySvc,
		transportMgr:  transportMgr,
		encryptionSvc: encryptionSvc,
		db:            db,
		logger:        logger,
	}
}

// Send sends an email with full validation and processing
func (s *Service) Send(ctx context.Context, req *SendRequest) (*SendResult, error) {
	sendStart := time.Now()
	result := &SendResult{}

	// 1. Validate sender has a unified address
	senderAddr, err := s.identitySvc.ValidateSender(ctx, req.SenderNpub)
	if err != nil {
		return nil, fmt.Errorf("sender validation failed: %w", err)
	}

	s.logger.Debug("Sender validated",
		zap.String("email", senderAddr.Email),
		zap.String("npub", req.SenderNpub[:16]+"..."))

	// 2. Resolve recipients for encryption capability
	allRecipients := append(append(req.To, req.CC...), req.BCC...)
	resolvedRecipients, err := s.identitySvc.ResolveRecipients(ctx, allRecipients)
	if err != nil {
		return nil, fmt.Errorf("recipient resolution failed: %w", err)
	}

	// Merge resolved pubkeys with provided ones
	recipientPubkeys := make(map[string]string)
	for email, resolved := range resolvedRecipients {
		if resolved.Npub != "" {
			recipientPubkeys[email] = resolved.Npub
		}
	}
	// Client-provided pubkeys override
	for email, pubkey := range req.RecipientPubkeys {
		if pubkey != "" {
			recipientPubkeys[email] = pubkey
		}
	}

	// 3. Determine body content based on encryption mode
	body := req.Body
	isPreEncrypted := false

	switch req.EncryptionMode {
	case encryption.ModeClientSide:
		// NIP-07: client already encrypted
		if req.PreEncryptedBody == "" {
			return nil, fmt.Errorf("pre-encrypted body required for client-side encryption mode")
		}
		body = req.PreEncryptedBody
		isPreEncrypted = true
		s.logger.Debug("Using pre-encrypted body (NIP-07)")

	case encryption.ModeServerSide:
		// NIP-46: we'll encrypt via transport layer
		s.logger.Debug("Server-side encryption requested (NIP-46)")

	case encryption.ModeNone:
		// No encryption
		s.logger.Debug("Sending unencrypted")
	}

	// 4. Build transport message
	msg := &transport.Message{
		FromAddress:         senderAddr.Email,
		ToAddresses:         req.To,
		CCAddresses:         req.CC,
		BCCAddresses:        req.BCC,
		SenderPubkey:        req.SenderNpub,
		RecipientPubkeys:    recipientPubkeys,
		Subject:             req.Subject,
		Body:                body,
		HTMLBody:            req.HTMLBody,
		IsPreEncrypted:      isPreEncrypted,
		EncryptionRequested: req.EncryptionMode == encryption.ModeServerSide,
		InReplyTo:           req.InReplyTo,
		References:          req.References,
		PreferredTransport:  req.PreferredTransport,
	}

	// 5. Send via transport manager
	deliveryResult, err := s.transportMgr.Send(ctx, msg)
	if err != nil {
		// Record failure metrics
		metrics.EmailsSentTotal.WithLabelValues("smtp", "false", "failure").Inc()
		metrics.EmailSendDuration.WithLabelValues("smtp").Observe(time.Since(sendStart).Seconds())
		return nil, fmt.Errorf("delivery failed: %w", err)
	}

	result.Success = deliveryResult.Success
	result.MessageID = deliveryResult.MessageID

	// Convert recipient results
	for _, r := range deliveryResult.Recipients {
		rr := RecipientSendResult{
			Email:     r.Address,
			Success:   r.Success,
			Encrypted: r.Encrypted,
		}
		if r.Error != nil {
			rr.Error = r.Error.Error()
		}
		result.Recipients = append(result.Recipients, rr)
	}

	// 6. Store in database
	if deliveryResult.Success {
		// Find recipient's user ID if they're internal
		recipientUserID := ""
		for _, addr := range req.To {
			if identity.ClassifyAddress(addr) == identity.AddressTypeInternal {
				// For internal recipients, they would receive via their own inbox
				// The email is stored for the sender's sent folder
				break
			}
		}

		email := &storage.Email{
			UserID:      "", // Will be set by GetUserByNpub
			MessageID:   stringPtr(deliveryResult.MessageID),
			FromAddress: senderAddr.Email,
			ToAddress:   req.To[0], // Primary recipient
			Subject:     req.Subject,
			Body:        body, // Store encrypted body if encrypted
			IsEncrypted: isPreEncrypted || req.EncryptionMode == encryption.ModeServerSide,
			SenderNpub:  stringPtr(req.SenderNpub),
			Direction:   "sent",
			Folder:      "sent",
			Status:      "active",
		}

		// Get sender's user record
		user, err := s.db.GetUserByNpub(ctx, req.SenderNpub)
		if err == nil && user != nil {
			email.UserID = user.ID
		}

		// Set recipient pubkey if available
		if pubkey, ok := recipientPubkeys[req.To[0]]; ok {
			email.RecipientNpub = stringPtr(pubkey)
		}

		if err := s.db.CreateEmail(ctx, email); err != nil {
			s.logger.Warn("Failed to store sent email in database",
				zap.Error(err),
				zap.String("message_id", deliveryResult.MessageID))
			// Don't fail the send - email was delivered
		} else {
			result.EmailID = email.ID
		}

		// Store for recipient if internal
		_ = recipientUserID // TODO: Create copy for internal recipients
	}

	if deliveryResult.Error != nil {
		result.Error = deliveryResult.Error.Error()
	}

	// Record metrics
	encrypted := isPreEncrypted || req.EncryptionMode == encryption.ModeServerSide
	encryptedStr := "false"
	if encrypted {
		encryptedStr = "true"
	}
	statusStr := "failure"
	if result.Success {
		statusStr = "success"
	}
	metrics.EmailsSentTotal.WithLabelValues("smtp", encryptedStr, statusStr).Inc()
	metrics.EmailSendDuration.WithLabelValues("smtp").Observe(time.Since(sendStart).Seconds())

	s.logger.Info("Email send completed",
		zap.Bool("success", result.Success),
		zap.String("message_id", result.MessageID),
		zap.Int("recipients", len(result.Recipients)))

	return result, nil
}

// GetEmail retrieves an email with decryption handling
func (s *Service) GetEmail(ctx context.Context, userNpub, emailID string) (*GetEmailResult, error) {
	// Validate user
	_, err := s.identitySvc.ValidateSender(ctx, userNpub)
	if err != nil {
		return nil, fmt.Errorf("user validation failed: %w", err)
	}

	// Get user record
	user, err := s.db.GetUserByNpub(ctx, userNpub)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}

	// Get email
	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return nil, fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return nil, fmt.Errorf("email not found")
	}

	// Verify ownership
	if email.UserID != user.ID {
		return nil, fmt.Errorf("access denied")
	}

	// Get sender pubkey from nullable field
	senderPubkey := ""
	if email.SenderNpub != nil {
		senderPubkey = *email.SenderNpub
	}

	// Get message ID from nullable field
	messageID := ""
	if email.MessageID != nil {
		messageID = *email.MessageID
	}

	result := &GetEmailResult{
		ID:           email.ID,
		MessageID:    messageID,
		From:         email.FromAddress,
		To:           email.ToAddress,
		Subject:      email.Subject,
		IsEncrypted:  email.IsEncrypted,
		SenderPubkey: senderPubkey,
		Folder:       email.Folder,
		CreatedAt:    email.CreatedAt,
	}

	if email.ReadAt != nil {
		result.ReadAt = email.ReadAt
	}

	// Handle decryption
	if email.IsEncrypted {
		// Determine encryption mode - for now assume server-side if we have sender npub
		mode := encryption.ModeServerSide
		if senderPubkey == "" {
			mode = encryption.ModeClientSide
		}
		result.EncryptionMode = mode

		if mode == encryption.ModeClientSide {
			// Client must decrypt - return ciphertext
			result.RequiresClientDecryption = true
			result.EncryptedBody = email.Body
		} else if mode == encryption.ModeServerSide && s.encryptionSvc != nil {
			// Try server-side decryption
			decryptReq := &encryption.DecryptionRequest{
				Ciphertext:      email.Body,
				RecipientPubkey: userNpub,
				SenderPubkey:    senderPubkey,
				Mode:            mode,
			}

			decryptResult, err := s.encryptionSvc.DecryptForRecipient(ctx, decryptReq)
			if err != nil {
				s.logger.Warn("Server-side decryption failed",
					zap.String("email_id", emailID),
					zap.Error(err))
				// Return encrypted body for client to try
				result.RequiresClientDecryption = true
				result.EncryptedBody = email.Body
			} else if decryptResult.RequiresClientDecryption {
				result.RequiresClientDecryption = true
				result.EncryptedBody = decryptResult.Ciphertext
			} else {
				result.Body = decryptResult.Plaintext
			}
		} else {
			// Unknown mode or no encryption service - return ciphertext
			result.RequiresClientDecryption = true
			result.EncryptedBody = email.Body
		}
	} else {
		// Not encrypted
		result.Body = email.Body
	}

	// Mark as read if incoming
	if email.Direction == "received" && email.ReadAt == nil {
		now := time.Now()
		email.ReadAt = &now
		if err := s.db.UpdateEmail(ctx, email); err != nil {
			s.logger.Warn("Failed to mark email as read", zap.Error(err))
		}
		result.ReadAt = &now
	}

	return result, nil
}

// GetEmailResult contains the result of retrieving an email
type GetEmailResult struct {
	ID             string
	MessageID      string
	From           string
	To             string
	Subject        string
	Body           string // Plaintext if decrypted or unencrypted
	EncryptedBody  string // Ciphertext if requires client decryption
	IsEncrypted    bool
	EncryptionMode encryption.EncryptionMode

	RequiresClientDecryption bool
	SenderPubkey             string

	Folder    string
	CreatedAt time.Time
	ReadAt    *time.Time
}

// ListEmails retrieves a list of emails for a user
func (s *Service) ListEmails(ctx context.Context, userNpub string, filter *storage.EmailFilter, opts storage.ListOptions) ([]*storage.Email, int, error) {
	// Get user record
	user, err := s.db.GetUserByNpub(ctx, userNpub)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, 0, fmt.Errorf("user not found")
	}

	// List emails
	emails, total, err := s.db.ListEmails(ctx, user.ID, filter, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list emails: %w", err)
	}

	return emails, total, nil
}

// DeleteEmail soft-deletes an email
func (s *Service) DeleteEmail(ctx context.Context, userNpub, emailID string) error {
	// Get user record
	user, err := s.db.GetUserByNpub(ctx, userNpub)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user not found")
	}

	// Get email to verify ownership
	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return fmt.Errorf("email not found")
	}
	if email.UserID != user.ID {
		return fmt.Errorf("access denied")
	}

	// Soft delete
	return s.db.DeleteEmail(ctx, emailID)
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func stringPtr(s string) *string {
	return &s
}
