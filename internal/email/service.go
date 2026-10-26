// Package email provides the core email service that coordinates
// identity validation, encryption, storage, and transport.
package email

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
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

	// From optionally selects which owned address to send as. Empty means the
	// sender's primary address. Must be an active address SenderNpub owns.
	From string

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
	// UploadAuth is the sender's own signed Blossom upload authorization for
	// Data (client-side mode only).
	UploadAuth *nostr.Event
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

	// sendGate enforces tier send-rights, per-account send state, and outbound
	// rate limits. Nil disables enforcement (self-hosted / tests).
	sendGate *SendGate

	// queue provides durable store-and-forward: transient delivery failures are
	// retried from it, and the abuse ladder holds/releases per account. Nil
	// falls back to fire-and-forget delivery.
	queue *transport.OutboundQueue
}

// WithQueue enables durable outbound queueing. Returns the service for chaining.
func (s *Service) WithQueue(q *transport.OutboundQueue) *Service {
	s.queue = q
	return s
}

// HoldSender parks a sender's undelivered mail (suspend-ladder hold rung).
func (s *Service) HoldSender(ctx context.Context, pubkey string) (int64, error) {
	if s.queue == nil {
		return 0, nil
	}
	return s.queue.HoldForSender(ctx, pubkey)
}

// ReleaseSender returns a sender's held mail to the delivery pool.
func (s *Service) ReleaseSender(ctx context.Context, pubkey string) (int64, error) {
	if s.queue == nil {
		return 0, nil
	}
	return s.queue.ReleaseForSender(ctx, pubkey)
}

// WithSendGate enables outbound abuse controls. Returns the service for chaining.
func (s *Service) WithSendGate(g *SendGate) *Service {
	s.sendGate = g
	return s
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

	// 1. Resolve and authorize the send-from address. An explicit From must be
	// an address this pubkey owns (any active alias); empty falls back to
	// their primary address.
	senderAddr, err := s.identitySvc.ResolveFromAddress(ctx, req.SenderNpub, req.From)
	if err != nil {
		return nil, fmt.Errorf("sender validation failed: %w", err)
	}

	s.logger.Debug("Sender validated",
		zap.String("email", senderAddr.Email),
		zap.String("npub", req.SenderNpub[:16]+"..."))

	// 1b. Abuse controls: tier send-rights (anonymous = receive-only), account
	// send state (suspend ladder), and outbound rate limits. Checked before any
	// delivery work so a denied send costs nothing downstream.
	if s.sendGate != nil {
		recipients := len(req.To) + len(req.CC) + len(req.BCC)
		size := int64(len(req.Body) + len(req.HTMLBody) + len(req.PreEncryptedBody))
		for _, a := range req.Attachments {
			size += int64(len(a.Data))
		}
		if err := s.sendGate.Check(ctx, req.SenderNpub, recipients, size); err != nil {
			return nil, err
		}
	}

	var clientEncryptedFor map[string]bool
	// 1c. Client-side (end-to-end) mode promises the server never sees
	// plaintext. The body is checked above as required; attachments must be
	// ciphertext too, or they would be relayed in the outbound MIME and
	// uploaded to Blossom as readable files.
	if req.EncryptionMode == encryption.ModeClientSide {
		if err := checkClientBodyEncrypted(req.PreEncryptedBody); err != nil {
			return nil, err
		}
		if err := checkClientAttachmentsEncrypted(req.Attachments); err != nil {
			return nil, err
		}
		if err := checkClientUploadAuth(req.SenderNpub, req.Attachments, time.Now()); err != nil {
			return nil, err
		}
		if err := checkClientNoBCC(req); err != nil {
			return nil, err
		}
		covered, err := checkClientRecipientsCovered(req)
		if err != nil {
			return nil, err
		}
		clientEncryptedFor = covered
	}

	// 2. Resolve recipients for encryption capability
	allRecipients := make([]string, 0, len(req.To)+len(req.CC)+len(req.BCC))
	allRecipients = append(allRecipients, req.To...)
	allRecipients = append(allRecipients, req.CC...)
	allRecipients = append(allRecipients, req.BCC...)
	resolvedRecipients, err := s.identitySvc.ResolveRecipients(ctx, allRecipients)
	if err != nil {
		return nil, fmt.Errorf("recipient resolution failed: %w", err)
	}

	// Client mode: the key the client wrapped to must be the key this server
	// resolves for the address, or "encrypted" would be the client's word.
	if req.EncryptionMode == encryption.ModeClientSide {
		resolvedKeys := make(map[string]string, len(resolvedRecipients))
		for addr, r := range resolvedRecipients {
			if r != nil {
				resolvedKeys[addr] = r.Npub
			}
		}
		if err := checkClientKeysMatchResolved(req, resolvedKeys); err != nil {
			return nil, err
		}
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

	// Server-side mode never falls back to plaintext: refuse what its one
	// encrypted body cannot cover before anything is sent.
	if req.EncryptionMode == encryption.ModeServerSide {
		if err := checkServerModeSendable(req, recipientPubkeys); err != nil {
			return nil, err
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
		Subject:             outerSubject(req),
		Body:                body,
		HTMLBody:            req.HTMLBody,
		IsPreEncrypted:      isPreEncrypted,
		EncryptedRecipients: clientEncryptedFor,
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
			MailboxPubkey:  req.SenderNpub,
			MessageID:      nonEmptyPtr(deliveryResult.MessageID),
			FromAddress:    senderAddr.Email,
			ToAddress:      req.To[0], // Primary recipient
			Subject:        outerSubject(req),
			Body:           storedBody,
			IsEncrypted:    storedMode != string(encryption.ModeNone),
			EncryptionMode: stringPtr(storedMode),
			SenderNpub:     stringPtr(req.SenderNpub),
			Direction:      "sent",
			Folder:         "sent",
			Status:         "active",
		}
		if req.InReplyTo != "" {
			email.InReplyTo = stringPtr(req.InReplyTo)
		}
		if len(req.References) > 0 {
			refs := strings.Join(req.References, " ")
			email.References = stringPtr(refs)
		}

		// The mailbox is keyed by the sender's pubkey; make sure it exists so
		// the emails FK resolves. Sender eligibility was already validated
		// against the shared addresses table above.
		if _, err := s.db.EnsureMailbox(ctx, req.SenderNpub); err != nil {
			s.logger.Warn("Failed to ensure sender mailbox",
				zap.Error(err), zap.String("pubkey", req.SenderNpub))
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


	// Get email
	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return nil, fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return nil, fmt.Errorf("email not found")
	}

	// Verify ownership
	if email.MailboxPubkey != userNpub {
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

	inReplyTo := ""
	if email.InReplyTo != nil {
		inReplyTo = *email.InReplyTo
	}
	references := ""
	if email.References != nil {
		references = *email.References
	}
	cc := ""
	if email.CC != nil {
		cc = *email.CC
	}

	result := &GetEmailResult{
		ID:              email.ID,
		MessageID:       messageID,
		InReplyTo:       inReplyTo,
		References:      references,
		From:            email.FromAddress,
		To:              email.ToAddress,
		CC:              cc,
		Subject:         email.Subject,
		IsEncrypted:     email.IsEncrypted,
		SenderPubkey:    senderPubkey,
		Folder:          email.Folder,
		Labels:          email.Labels,
		CreatedAt:       email.CreatedAt,
		NostrVerified:   email.NostrVerified,
		NostrVerifiedAt: email.NostrVerifiedAt,
	}

	if email.ReadAt != nil {
		result.ReadAt = email.ReadAt
	}

	// Fetch attachments (metadata only; no body decryption here)
	if atts, err := s.db.GetAttachmentsByEmail(ctx, email.ID); err == nil {
		result.Attachments = make([]storage.Attachment, 0, len(atts))
		for _, a := range atts {
			if a != nil {
				result.Attachments = append(result.Attachments, *a)
			}
		}
	}

	// Handle decryption based on the mode the body was actually stored under.
	// Legacy rows (pre-migration) have a nil EncryptionMode; fall back to the
	// IsEncrypted flag and never attempt server-side decryption on an unknown
	// mode (which would feed possibly-plaintext bytes to the bunker).
	mode := storedMode(email)
	result.EncryptionMode = mode
	result.HTMLBody = htmlBodyFor(email, mode)

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
			// When there's no live bunker session (expected for users whose
			// signer-as-bunker handshake hasn't completed yet), fall back to
			// client-side decryption gracefully rather than logging a warning.
			if errors.Is(err, encryption.ErrNoSignerConnection) {
				s.logger.Debug("No bunker connection — returning ciphertext for client decryption",
					zap.String("email_id", emailID))
			} else {
				s.logger.Warn("Server-side decryption failed",
					zap.String("email_id", emailID),
					zap.Error(err))
			}
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

// htmlBodyFor returns the stored HTML version of an unencrypted message, or
// "" (encrypted messages never hand out stored HTML: it is not ciphertext the
// client can open, and nothing encrypted should be rendered unasked).
func htmlBodyFor(email *storage.Email, mode encryption.EncryptionMode) string {
	if email.HTMLBody == nil || email.IsEncrypted || mode != encryption.ModeNone {
		return ""
	}
	return *email.HTMLBody
}

// GetEmailResult contains the result of retrieving an email
type GetEmailResult struct {
	ID             string
	MessageID      string
	InReplyTo      string
	References     string // space-separated
	From           string
	To             string
	CC             string
	Subject        string
	Body           string // Plaintext if decrypted or unencrypted
	HTMLBody       string // HTML version, unencrypted messages only
	EncryptedBody  string // Ciphertext if requires client decryption
	IsEncrypted    bool
	EncryptionMode encryption.EncryptionMode

	RequiresClientDecryption bool
	SenderPubkey             string

	Folder    string
	Labels    []string
	CreatedAt time.Time
	ReadAt    *time.Time

	// Attachments (lightweight list for the detail view header)
	Attachments []storage.Attachment

	// Nostr signature verification (RFC-002)
	NostrVerified   bool
	NostrVerifiedAt *time.Time
}

// ListEmails retrieves a list of emails for a user
func (s *Service) ListEmails(ctx context.Context, userNpub string, filter *storage.EmailFilter, opts storage.ListOptions) ([]*storage.Email, int, error) {
	// The mailbox IS the pubkey — no identity lookup needed. An unprovisioned
	// pubkey simply has no rows, which naturally yields an empty inbox.
	emails, total, err := s.db.ListEmails(ctx, userNpub, filter, opts)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list emails: %w", err)
	}

	return emails, total, nil
}

// DeleteEmail soft-deletes an email
func (s *Service) DeleteEmail(ctx context.Context, userNpub, emailID string) error {

	// Get email to verify ownership
	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return fmt.Errorf("email not found")
	}
	if email.MailboxPubkey != userNpub {
		return fmt.Errorf("access denied")
	}

	// Soft delete
	return s.db.DeleteEmail(ctx, emailID)
}

// ArchiveEmail moves an email to the archive folder.
func (s *Service) ArchiveEmail(ctx context.Context, userNpub, emailID string) error {
	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return fmt.Errorf("email not found")
	}
	if email.MailboxPubkey != userNpub {
		return fmt.Errorf("access denied")
	}
	return s.db.MoveEmailToFolder(ctx, emailID, "archive")
}

// MarkEmailRead marks a single email as read (idempotent).
func (s *Service) MarkEmailRead(ctx context.Context, userNpub, emailID string) error {
	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return fmt.Errorf("email not found")
	}
	if email.MailboxPubkey != userNpub {
		return fmt.Errorf("access denied")
	}
	return s.db.MarkEmailAsRead(ctx, emailID)
}

// MarkEmailUnread marks a single email as unread.
func (s *Service) MarkEmailUnread(ctx context.Context, userNpub, emailID string) error {
	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return fmt.Errorf("email not found")
	}
	if email.MailboxPubkey != userNpub {
		return fmt.Errorf("access denied")
	}
	return s.db.MarkEmailAsUnread(ctx, emailID)
}

// ToggleStar adds or removes the "\\Starred" label on an email.
func (s *Service) ToggleStar(ctx context.Context, userNpub, emailID string, star bool) error {
	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return fmt.Errorf("email not found")
	}
	if email.MailboxPubkey != userNpub {
		return fmt.Errorf("access denied")
	}
	const starredLabel = `\Starred`
	if star {
		return s.db.AddEmailLabel(ctx, emailID, starredLabel)
	}
	return s.db.RemoveEmailLabel(ctx, emailID, starredLabel)
}

// AddLabel adds a user-defined label to an email.
func (s *Service) AddLabel(ctx context.Context, userNpub, emailID, label string) error {
	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return fmt.Errorf("email not found")
	}
	if email.MailboxPubkey != userNpub {
		return fmt.Errorf("access denied")
	}
	return s.db.AddEmailLabel(ctx, emailID, label)
}

// RemoveLabel removes a label from an email.
func (s *Service) RemoveLabel(ctx context.Context, userNpub, emailID, label string) error {
	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return fmt.Errorf("email not found")
	}
	if email.MailboxPubkey != userNpub {
		return fmt.Errorf("access denied")
	}
	return s.db.RemoveEmailLabel(ctx, emailID, label)
}

// MoveEmail moves an email to a different folder.
func (s *Service) MoveEmail(ctx context.Context, userNpub, emailID, folder string) error {
	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return fmt.Errorf("email not found")
	}
	if email.MailboxPubkey != userNpub {
		return fmt.Errorf("access denied")
	}
	return s.db.MoveEmailToFolder(ctx, emailID, folder)
}

// BulkActionRequest describes a bulk operation on multiple emails.
type BulkActionRequest struct {
	IDs    []string
	Action string // read, unread, archive, delete, move, star, unstar
	Folder string // for "move" action
}

// BulkAction performs a bulk operation, enforcing ownership for each email.
func (s *Service) BulkAction(ctx context.Context, userNpub string, req *BulkActionRequest) error {
	if len(req.IDs) == 0 {
		return fmt.Errorf("no email IDs provided")
	}
	// Ownership check: load all emails and verify mailbox matches.
	// We do this in one pass before any writes.
	for _, id := range req.IDs {
		e, err := s.db.GetEmail(ctx, id)
		if err != nil {
			return fmt.Errorf("failed to get email %s: %w", id, err)
		}
		if e == nil || e.MailboxPubkey != userNpub {
			return fmt.Errorf("access denied for email %s", id)
		}
	}

	switch req.Action {
	case "read":
		return s.db.BulkMarkRead(ctx, req.IDs)
	case "unread":
		return s.db.BulkMarkUnread(ctx, req.IDs)
	case "archive":
		return s.db.BulkArchive(ctx, req.IDs)
	case "delete":
		return s.db.BulkDelete(ctx, req.IDs)
	case "move":
		if req.Folder == "" {
			return fmt.Errorf("folder required for move action")
		}
		return s.db.BulkMoveToFolder(ctx, req.IDs, req.Folder)
	case "star":
		for _, id := range req.IDs {
			if err := s.db.AddEmailLabel(ctx, id, `\Starred`); err != nil {
				return err
			}
		}
		return nil
	case "unstar":
		for _, id := range req.IDs {
			if err := s.db.RemoveEmailLabel(ctx, id, `\Starred`); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown bulk action: %s", req.Action)
	}
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

	email, err := s.db.GetEmail(ctx, emailID)
	if err != nil {
		return nil, fmt.Errorf("failed to get email: %w", err)
	}
	if email == nil {
		return nil, fmt.Errorf("email not found")
	}
	if email.MailboxPubkey != userNpub {
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
	serverAuth := blossom.NewEventAuthSigner(senderNpub, func(ctx context.Context, ev *nostr.Event) error {
		return s.encryptionSvc.SignEventForUser(ctx, senderNpub, ev)
	})

	for _, att := range atts {
		blob, err := s.attachmentBlob(ctx, senderNpub, mode, att)
		if err != nil {
			s.logger.Warn("Failed to prepare attachment for Blossom",
				zap.String("filename", att.Filename), zap.Error(err))
			continue
		}
		authSigner, ok := uploadAuthFor(mode, serverAuth, att, blob)
		if !ok {
			s.logger.Info("Client did not authorize storing this attachment; delivered by mail only",
				zap.String("filename", att.Filename))
			continue
		}
		client := blossom.NewClient(authSigner, s.logger)
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

// EncryptedSubjectPlaceholder is the only subject a client-side (end-to-end)
// message carries outside its ciphertext. The real subject travels inside the
// encrypted body as a protected header (see ui/src/lib/protected-headers.ts).
const EncryptedSubjectPlaceholder = "Encrypted message"

// outerSubject is the subject that goes on the wire and into storage. In
// client-side mode it is always the placeholder, whatever the client sent, so
// a client that still puts the real subject in the request cannot leak it into
// the outbound headers or the stored copy.
func outerSubject(req *SendRequest) string {
	if req.EncryptionMode == encryption.ModeClientSide {
		return EncryptedSubjectPlaceholder
	}
	return req.Subject
}

// ErrClientAttachmentNotEncrypted is returned when a client-side (end-to-end)
// send carries an attachment that is not a client-encrypted envelope.
var ErrClientAttachmentNotEncrypted = errors.New("client-side encryption requires every attachment to be encrypted by the client")

// checkClientAttachmentsEncrypted refuses a client-side send if any attachment
// is not a client-encrypted attachment envelope.
func checkClientAttachmentsEncrypted(atts []AttachmentInput) error {
	for _, a := range atts {
		if !encryption.IsEncryptedAttachment(a.Data) {
			return fmt.Errorf("%w: %s", ErrClientAttachmentNotEncrypted, a.Filename)
		}
	}
	return nil
}

// ErrClientBodyNotEncrypted is returned when a client-side send's body is not
// ciphertext: neither a body envelope nor (older format) a NIP-44 payload.
var ErrClientBodyNotEncrypted = errors.New("client-side encryption requires the body to be encrypted by the client")

// checkClientBodyEncrypted refuses a client-side body that is not ciphertext.
// Both the body envelope and the older NIP-44-to-recipient payload are
// accepted, so older clients keep working.
func checkClientBodyEncrypted(body string) error {
	if encryption.IsEncryptedBody(body) || encryption.IsNIP44Payload(body) {
		return nil
	}
	return ErrClientBodyNotEncrypted
}

// ErrClientRecipientNotEncrypted is returned when a client-side send leaves a
// recipient (To, CC or BCC) without a wrapped key: every recipient gets the
// same message, and one with no key could not open it.
var ErrClientRecipientNotEncrypted = errors.New("client-side encryption needs a key for every recipient")

// checkClientRecipientsCovered refuses a client-side send unless every
// recipient's pubkey (req.RecipientPubkeys) holds a wrapped key in the body
// envelope and in every attachment envelope, naming the recipients who do
// not. It returns the covered addresses, which is who can decrypt.
//
// The older NIP-44 body format is encrypted to a single recipient and does
// not say to whom, so it is accepted only for a single recipient with a key.
func checkClientRecipientsCovered(req *SendRequest) (map[string]bool, error) {
	var all []string
	seen := map[string]bool{}
	for _, list := range [][]string{req.To, req.CC, req.BCC} {
		for _, a := range list {
			if !seen[a] {
				seen[a] = true
				all = append(all, a)
			}
		}
	}
	// Addresses compare case-insensitively: "Bob@x" in To and "bob@x" in
	// RecipientPubkeys are the same mailbox for this check.
	keyByAddr := make(map[string]string, len(req.RecipientPubkeys))
	for a, pk := range req.RecipientPubkeys {
		keyByAddr[strings.ToLower(strings.TrimSpace(a))] = strings.ToLower(pk)
	}
	pubkey := func(addr string) string { return keyByAddr[strings.ToLower(strings.TrimSpace(addr))] }

	bodyHolders, isEnvelope := encryption.BodyKeyHolders(req.PreEncryptedBody)
	if !isEnvelope {
		if len(all) != 1 || pubkey(all[0]) == "" {
			return nil, fmt.Errorf("%w: the older single-recipient body format cannot be read by %s",
				ErrClientRecipientNotEncrypted, strings.Join(all, ", "))
		}
		// Trust the one recipient's key for the body; attachments still say.
		bodyHolders = map[string]bool{pubkey(all[0]): true}
	}
	attHolders := make([]map[string]bool, 0, len(req.Attachments))
	for _, a := range req.Attachments {
		h, _ := encryption.AttachmentKeyHolders(a.Data) // shape already checked
		attHolders = append(attHolders, h)
	}

	covered := make(map[string]bool, len(all))
	var missing []string
	for _, addr := range all {
		pk := pubkey(addr)
		ok := pk != "" && bodyHolders[pk]
		for _, h := range attHolders {
			ok = ok && h[pk]
		}
		if ok {
			covered[addr] = true
		} else {
			missing = append(missing, addr)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: no key for %s", ErrClientRecipientNotEncrypted, strings.Join(missing, ", "))
	}
	return covered, nil
}

// ErrClientBCCUnsupported is returned for a client-side send with BCC
// recipients. Every recipient opens the same body envelope, whose key map is
// keyed by recipient pubkey, so wrapping to a BCC would show To and CC who was
// BCC'd. Refused until each BCC gets its own envelope.
var ErrClientBCCUnsupported = errors.New("BCC is not supported with end-to-end encryption yet: every recipient could see who was BCC'd")

func checkClientNoBCC(req *SendRequest) error {
	if len(req.BCC) > 0 {
		return ErrClientBCCUnsupported
	}
	return nil
}

// ErrClientRecipientKeyMismatch is returned when a client-side send names a
// recipient key the server cannot confirm is that recipient's.
var ErrClientRecipientKeyMismatch = errors.New("client-side encryption used a key that is not the recipient's")

// checkClientKeysMatchResolved refuses a client-side send unless the pubkey
// the client supplied for every To and CC address equals the one the server
// resolves for it (resolved: address -> hex pubkey, "" or absent if none),
// naming every recipient that does not. Without it a client could wrap to any
// key and still have the recipient reported as encrypted.
func checkClientKeysMatchResolved(req *SendRequest, resolved map[string]string) error {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	claimed := make(map[string]string, len(req.RecipientPubkeys))
	for a, pk := range req.RecipientPubkeys {
		claimed[norm(a)] = norm(pk)
	}
	var bad []string
	for _, list := range [][]string{req.To, req.CC} {
		for _, addr := range list {
			want := norm(resolved[addr])
			if want == "" || claimed[norm(addr)] != want {
				bad = append(bad, addr)
			}
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("%w: %s", ErrClientRecipientKeyMismatch, strings.Join(bad, ", "))
	}
	return nil
}

// ErrInvalidUploadAuth is returned when a client-side send carries a Blossom
// upload authorization that is not the sender's own, valid one for the bytes.
var ErrInvalidUploadAuth = errors.New("attachment upload authorization is invalid")

// checkClientUploadAuth validates every client-signed upload authorization a
// client-side send carries, before anything is delivered, so a bad one is a
// clear 400 rather than a silently unstored attachment. Attachments without
// one are allowed: they are delivered by mail and not stored.
func checkClientUploadAuth(senderPubkey string, atts []AttachmentInput, now time.Time) error {
	for _, a := range atts {
		if a.UploadAuth == nil {
			continue
		}
		if err := blossom.ValidateUploadAuth(a.UploadAuth, senderPubkey, blossom.BlobHash(a.Data), now); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalidUploadAuth, a.Filename, err)
		}
	}
	return nil
}

// uploadAuthFor picks who authorizes storing an attachment. Client-side mode
// uses only the sender's own signed authorization (client-side signing: the
// server never signs for that user); without one the attachment is not
// stored. Other modes use the server-held signer as before.
func uploadAuthFor(mode encryption.EncryptionMode, serverAuth blossom.AuthSigner, att AttachmentInput, blob []byte) (blossom.AuthSigner, bool) {
	if mode != encryption.ModeClientSide {
		return serverAuth, true
	}
	if att.UploadAuth == nil {
		return nil, false
	}
	return blossom.NewPresignedUploadAuth(att.UploadAuth, blossom.BlobHash(blob)), true
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
	if mode == encryption.ModeClientSide {
		// Already ciphertext from the client; Send refuses anything else, and
		// this re-check keeps a plaintext file off Blossom if a future caller
		// skips Send.
		if !encryption.IsEncryptedAttachment(att.Data) {
			return nil, fmt.Errorf("%w: %s", ErrClientAttachmentNotEncrypted, att.Filename)
		}
		return att.Data, nil
	}
	if mode != encryption.ModeServerSide {
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

func stringPtr(s string) *string {
	return &s
}

// nonEmptyPtr stores a missing value as NULL rather than "": message_id is
// unique, and an empty string would collide with every other empty one.
func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Server-side (NIP-46) mode encrypts ONE body to ONE recipient's key on the
// server. It used to send whatever that could not cover in plaintext (no key,
// attachments) or undecryptable (extra recipients) while the user believed the
// message was encrypted. These refuse the send instead.
var (
	ErrServerModeOneRecipient = errors.New("server-side encryption can send to exactly one recipient; remove the others or send without encryption")
	ErrServerRecipientNoKey   = errors.New("server-side encryption needs the recipient's Nostr key, and none was found; send without encryption instead")
	ErrServerModeAttachments  = errors.New("server-side encryption cannot encrypt attachments; remove them or send without encryption")
)

// checkServerModeSendable refuses a server-side send that its single encrypted
// body cannot fully cover. resolved maps recipient address to Nostr pubkey.
func checkServerModeSendable(req *SendRequest, resolved map[string]string) error {
	if len(req.To) != 1 || len(req.CC) != 0 || len(req.BCC) != 0 {
		return ErrServerModeOneRecipient
	}
	if len(req.Attachments) != 0 {
		return ErrServerModeAttachments
	}
	if resolved[req.To[0]] == "" {
		return fmt.Errorf("%w (%s)", ErrServerRecipientNoKey, req.To[0])
	}
	return nil
}
