// Package email provides the core email service that coordinates
// identity validation, encryption, storage, and transport.
package email

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/blossom"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/identity"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/metrics"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/transport"
	"github.com/nbd-wtf/go-nostr"
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

	// Attachments to store on Blossom (optional)
	Attachments []AttachmentInput
}

// AttachmentInput is a single outgoing attachment to be offloaded to Blossom.
type AttachmentInput struct {
	Filename    string
	ContentType string
	// Data is the raw attachment bytes. For server-side mode it is encrypted
	// at rest before upload; for client-side mode it is treated as already
	// ciphertext; for no-encryption mode it is uploaded as-is.
	Data []byte
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
	identitySvc   *identity.Service
	transportMgr  *transport.Manager
	encryptionSvc *encryption.EncryptionService
	db            *storage.PostgreSQL
	logger        *zap.Logger

	// Blossom storage (optional; attachments are offloaded here when set)
	blossomServers    []blossom.Server
	blossomRedundancy int
}

// WithBlossom enables Blossom attachment offload using the given servers and
// upload redundancy. Returns the service for chaining.
func (s *Service) WithBlossom(servers []blossom.Server, redundancy int) *Service {
	s.blossomServers = servers
	s.blossomRedundancy = redundancy
	return s
}

// blossomEnabled reports whether attachment offload is configured.
func (s *Service) blossomEnabled() bool {
	return len(s.blossomServers) > 0 && s.encryptionSvc != nil
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
	allRecipients := make([]string, 0, len(req.To)+len(req.CC)+len(req.BCC))
	allRecipients = append(allRecipients, req.To...)
	allRecipients = append(allRecipients, req.CC...)
	allRecipients = append(allRecipients, req.BCC...)
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
		Attachments:         toTransportAttachments(req.Attachments),
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

		// Compute the at-rest representation of the body for the sender's
		// stored copy. The transport `body` above is sent to recipients
		// (encrypted per-recipient by the transport for server-side mode);
		// the stored copy is encrypted independently so it never sits in
		// plaintext when encryption was requested.
		storedBody, storedMode := s.bodyAtRest(ctx, req)

		email := &storage.Email{
			UserID:         "", // Will be set by GetUserByNpub
			MessageID:      stringPtr(deliveryResult.MessageID),
			FromAddress:    senderAddr.Email,
			ToAddress:      req.To[0], // Primary recipient
			Subject:        req.Subject,
			Body:           storedBody,
			IsEncrypted:    storedMode != string(encryption.ModeNone),
			EncryptionMode: stringPtr(storedMode),
			SenderNpub:     stringPtr(req.SenderNpub),
			Direction:      "sent",
			Folder:         "sent",
			Status:         "active",
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
			// Offload attachments to Blossom (best-effort; email is already sent).
			if s.blossomEnabled() && len(req.Attachments) > 0 {
				s.uploadAttachments(ctx, req.SenderNpub, req.EncryptionMode, email.ID, req.Attachments)
			}
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
		ID:              email.ID,
		MessageID:       messageID,
		From:            email.FromAddress,
		To:              email.ToAddress,
		Subject:         email.Subject,
		IsEncrypted:     email.IsEncrypted,
		SenderPubkey:    senderPubkey,
		Folder:          email.Folder,
		CreatedAt:       email.CreatedAt,
		NostrVerified:   email.NostrVerified,
		NostrVerifiedAt: email.NostrVerifiedAt,
	}

	if email.ReadAt != nil {
		result.ReadAt = email.ReadAt
	}

	// Handle decryption based on the mode the body was actually stored under.
	// Legacy rows (pre-migration) have a nil EncryptionMode; fall back to the
	// IsEncrypted flag and never attempt server-side decryption on an unknown
	// mode (which would feed possibly-plaintext bytes to the bunker).
	mode := storedMode(email)
	result.EncryptionMode = mode

	switch mode {
	case encryption.ModeNone:
		result.Body = email.Body

	case encryption.ModeClientSide:
		// Client must decrypt - return ciphertext.
		result.RequiresClientDecryption = true
		result.EncryptedBody = email.Body

	case encryption.ModeServerSide:
		if s.encryptionSvc == nil {
			result.RequiresClientDecryption = true
			result.EncryptedBody = email.Body
			break
		}
		// The stored copy is self-encrypted (sender<->sender); for a sent
		// email senderPubkey == the reading user.
		decryptResult, err := s.encryptionSvc.DecryptForRecipient(ctx, &encryption.DecryptionRequest{
			Ciphertext:      email.Body,
			RecipientPubkey: userNpub,
			SenderPubkey:    senderPubkey,
			Mode:            encryption.ModeServerSide,
		})
		if err != nil {
			s.logger.Warn("Server-side decryption failed",
				zap.String("email_id", emailID),
				zap.Error(err))
			result.RequiresClientDecryption = true
			result.EncryptedBody = email.Body
		} else if decryptResult.RequiresClientDecryption {
			result.RequiresClientDecryption = true
			result.EncryptedBody = decryptResult.Ciphertext
		} else {
			result.Body = decryptResult.Plaintext
		}
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

	// Nostr signature verification (RFC-002)
	NostrVerified   bool
	NostrVerifiedAt *time.Time
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

// GetAttachmentResult is the decrypted (or client-decryptable) attachment.
type GetAttachmentResult struct {
	Filename    string
	ContentType string

	// Data holds the decrypted bytes for server-side / unencrypted attachments.
	Data []byte

	// Ciphertext + RequiresClientDecryption are set for client-side mode, where
	// only the recipient/sender can decrypt with their own key.
	Ciphertext               string
	RequiresClientDecryption bool
}

// GetAttachment fetches an attachment blob from Blossom and decrypts it
// according to the parent email's encryption mode. Ownership is enforced via
// the parent email.
func (s *Service) GetAttachment(ctx context.Context, userNpub, emailID, attachmentID string) (*GetAttachmentResult, error) {
	if !s.blossomEnabled() {
		return nil, fmt.Errorf("blossom storage is not configured")
	}

	user, err := s.db.GetUserByNpub(ctx, userNpub)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("user not found")
	}

	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return nil, fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return nil, fmt.Errorf("email not found")
	}
	if email.UserID != user.ID {
		return nil, fmt.Errorf("access denied")
	}

	atts, err := s.db.GetAttachmentsByEmail(ctx, emailID)
	if err != nil {
		return nil, fmt.Errorf("failed to get attachments: %w", err)
	}
	var att *storage.Attachment
	for _, a := range atts {
		if a.ID == attachmentID {
			att = a
			break
		}
	}
	if att == nil {
		return nil, fmt.Errorf("attachment not found")
	}
	if att.BlossomSHA256 == nil || *att.BlossomSHA256 == "" {
		return nil, fmt.Errorf("attachment has no Blossom reference")
	}

	authSigner := blossom.NewEventAuthSigner(userNpub, func(ctx context.Context, ev *nostr.Event) error {
		return s.encryptionSvc.SignEventForUser(ctx, userNpub, ev)
	})
	client := blossom.NewClient(authSigner, s.logger)
	blob, err := client.Download(ctx, *att.BlossomSHA256, s.blossomServers)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch attachment from Blossom: %w", err)
	}

	res := &GetAttachmentResult{Filename: att.Filename}
	if att.ContentType != nil {
		res.ContentType = *att.ContentType
	}

	switch storedMode(email) {
	case encryption.ModeClientSide:
		res.RequiresClientDecryption = true
		res.Ciphertext = string(blob)
	case encryption.ModeServerSide:
		senderPubkey := ""
		if email.SenderNpub != nil {
			senderPubkey = *email.SenderNpub
		}
		dec, err := s.encryptionSvc.DecryptForRecipient(ctx, &encryption.DecryptionRequest{
			Ciphertext:      string(blob),
			RecipientPubkey: userNpub,
			SenderPubkey:    senderPubkey,
			Mode:            encryption.ModeServerSide,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt attachment: %w", err)
		}
		if dec.RequiresClientDecryption {
			res.RequiresClientDecryption = true
			res.Ciphertext = dec.Ciphertext
			break
		}
		data, err := base64.StdEncoding.DecodeString(dec.Plaintext)
		if err != nil {
			return nil, fmt.Errorf("failed to decode attachment bytes: %w", err)
		}
		res.Data = data
	default:
		res.Data = blob
	}
	return res, nil
}

// uploadAttachments encrypts (per the email's mode) and offloads each
// attachment to Blossom, persisting an Attachment row referencing the blob.
// Best-effort: per-attachment failures are logged and skipped (the email is
// already delivered). For NIP-07 (client-side) users the server has no signer,
// so Blossom auth signing fails and uploads are skipped — those clients upload
// their own attachments.
func (s *Service) uploadAttachments(ctx context.Context, senderNpub string, mode encryption.EncryptionMode, emailID string, atts []AttachmentInput) {
	authSigner := blossom.NewEventAuthSigner(senderNpub, func(ctx context.Context, ev *nostr.Event) error {
		return s.encryptionSvc.SignEventForUser(ctx, senderNpub, ev)
	})
	client := blossom.NewClient(authSigner, s.logger)

	for _, att := range atts {
		blob, err := s.attachmentBlob(ctx, senderNpub, mode, att)
		if err != nil {
			s.logger.Warn("Failed to prepare attachment for Blossom",
				zap.String("filename", att.Filename), zap.Error(err))
			continue
		}
		desc, err := client.Upload(ctx, blob, att.ContentType, s.blossomServers, s.blossomRedundancy)
		if err != nil {
			s.logger.Warn("Failed to upload attachment to Blossom",
				zap.String("filename", att.Filename), zap.Error(err))
			continue
		}

		size := int64(len(att.Data))
		contentType := att.ContentType
		url := ""
		if len(desc.Servers) > 0 {
			url = desc.Servers[0] + "/" + desc.SHA256
		}
		rec := &storage.Attachment{
			EmailID:       emailID,
			Filename:      att.Filename,
			ContentType:   &contentType,
			SizeBytes:     &size,
			BlossomSHA256: &desc.SHA256,
			BlossomURL:    &url,
		}
		if err := s.db.CreateAttachment(ctx, rec); err != nil {
			s.logger.Warn("Failed to persist attachment record",
				zap.String("filename", att.Filename), zap.Error(err))
		}
	}
}

// toTransportAttachments maps send-request attachments to transport MIME
// attachments delivered to recipients. The bytes are the caller-provided
// content (plaintext for server/none mode). Note: attachment MIME parts are
// not yet per-recipient encrypted — that mirrors the body's current
// first-recipient simplification and is a follow-up.
func toTransportAttachments(atts []AttachmentInput) []transport.Attachment {
	if len(atts) == 0 {
		return nil
	}
	out := make([]transport.Attachment, len(atts))
	for i, a := range atts {
		out[i] = transport.Attachment{Filename: a.Filename, ContentType: a.ContentType, Data: a.Data}
	}
	return out
}

// attachmentBlob returns the bytes to upload for an attachment given the
// email's encryption mode. Server-side mode NIP-44 self-encrypts the
// base64-encoded bytes (NIP-44 operates on text); client/none upload as-is.
func (s *Service) attachmentBlob(ctx context.Context, senderNpub string, mode encryption.EncryptionMode, att AttachmentInput) ([]byte, error) {
	if mode != encryption.ModeServerSide {
		// none: plaintext; client: already ciphertext from the client.
		return att.Data, nil
	}
	res, err := s.encryptionSvc.EncryptForRecipient(ctx, &encryption.EncryptionRequest{
		Plaintext:       base64.StdEncoding.EncodeToString(att.Data),
		SenderPubkey:    senderNpub,
		RecipientPubkey: senderNpub, // self-encrypt the sender's stored copy
		Mode:            encryption.ModeServerSide,
	})
	if err != nil {
		return nil, err
	}
	return []byte(res.Ciphertext), nil
}

// storedMode resolves the encryption mode an email's body was persisted under.
// Pre-migration rows have a nil EncryptionMode; fall back to the IsEncrypted
// flag, treating unknown-but-encrypted bodies as client-decryptable rather than
// risking a server-side decrypt of bytes that may not be ciphertext.
func storedMode(email *storage.Email) encryption.EncryptionMode {
	if email.EncryptionMode != nil && *email.EncryptionMode != "" {
		return encryption.EncryptionMode(*email.EncryptionMode)
	}
	if email.IsEncrypted {
		return encryption.ModeClientSide
	}
	return encryption.ModeNone
}

// bodyAtRest returns the body to persist for the sender's stored ("sent")
// copy and the encryption mode recorded alongside it. Plaintext is never
// stored when encryption was requested: server-side bodies are self-encrypted
// (sender<->sender) so the sender can later read their own copy, and if that
// encryption fails the body is dropped rather than persisted in the clear.
func (s *Service) bodyAtRest(ctx context.Context, req *SendRequest) (string, string) {
	switch req.EncryptionMode {
	case encryption.ModeClientSide:
		// Already ciphertext from the client (NIP-07).
		return req.PreEncryptedBody, string(encryption.ModeClientSide)

	case encryption.ModeServerSide:
		if s.encryptionSvc == nil {
			s.logger.Warn("No encryption service available; dropping sent-copy body to avoid storing plaintext")
			return "", string(encryption.ModeServerSide)
		}
		res, err := s.encryptionSvc.EncryptForRecipient(ctx, &encryption.EncryptionRequest{
			Plaintext:       req.Body,
			SenderPubkey:    req.SenderNpub,
			RecipientPubkey: req.SenderNpub, // self-encrypt the sender's stored copy
			Mode:            encryption.ModeServerSide,
		})
		if err != nil {
			s.logger.Warn("Failed to encrypt sent-copy body at rest; dropping body", zap.Error(err))
			return "", string(encryption.ModeServerSide)
		}
		return res.Ciphertext, string(encryption.ModeServerSide)

	default:
		return req.Body, string(encryption.ModeNone)
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func stringPtr(s string) *string {
	return &s
}
