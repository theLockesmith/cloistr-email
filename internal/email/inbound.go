// Package email provides email processing functionality.
package email

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/encryption"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/storage"
	"git.coldforge.xyz/coldforge/cloistr-email/internal/transport"
	"go.uber.org/zap"
)

// InboundProcessor handles incoming email messages
type InboundProcessor struct {
	db       *storage.PostgreSQL
	verifier *EmailVerifier
	logger   *zap.Logger
}

// NewInboundProcessor creates a new inbound processor
func NewInboundProcessor(db *storage.PostgreSQL, nip05Resolver *encryption.NIP05Resolver, logger *zap.Logger) *InboundProcessor {
	var verifier *EmailVerifier
	if nip05Resolver != nil {
		verifier = NewEmailVerifier(nip05Resolver, logger)
	}

	return &InboundProcessor{
		db:       db,
		verifier: verifier,
		logger:   logger,
	}
}

// HandleMessage implements transport.MessageHandler
func (p *InboundProcessor) HandleMessage(ctx context.Context, from string, to []string, data []byte) error {
	p.logger.Debug("Processing inbound message",
		zap.String("envelope_from", from),
		zap.Strings("envelope_to", to),
		zap.Int("size", len(data)))

	// Parse the message
	parsed, err := p.parseMessage(data)
	if err != nil {
		p.logger.Error("Failed to parse message", zap.Error(err))
		return transport.NewPermanentError(fmt.Errorf("invalid message format: %w", err))
	}

	// Use envelope from if header From is missing
	if parsed.From == "" {
		parsed.From = from
	}

	// Verify Nostr signature if present
	var verifyResult *VerificationResult
	if p.verifier != nil && parsed.NostrPubkey != "" {
		verifiable := &VerifiableEmail{
			Headers:            parsed.Headers,
			Body:               parsed.Body,
			NostrPubkey:        parsed.NostrPubkey,
			NostrSig:           parsed.NostrSig,
			NostrSignedHeaders: strings.Join(parsed.NostrHeaders, ";"),
			FromAddress:        parsed.From,
		}
		verifyResult = p.verifier.Verify(ctx, verifiable)
		p.logger.Debug("Nostr signature verification",
			zap.Bool("valid", verifyResult.Valid),
			zap.Bool("nip05_verified", verifyResult.NIP05Verified),
			zap.String("reason", verifyResult.Reason))
	}

	// Store the message for each recipient
	for _, recipient := range to {
		if err := p.storeForRecipient(ctx, parsed, recipient, verifyResult); err != nil {
			p.logger.Error("Failed to store message for recipient",
				zap.String("recipient", recipient),
				zap.Error(err))
			// Continue with other recipients
		}
	}

	return nil
}

// ValidateRecipient implements transport.RecipientValidator
func (p *InboundProcessor) ValidateRecipient(ctx context.Context, address string) error {
	// Check if user exists in our database
	user, err := p.db.GetUserByEmail(ctx, address)
	if err != nil {
		return fmt.Errorf("recipient lookup failed: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user not found: %s", address)
	}
	return nil
}

// ParsedMessage represents a parsed email message
type ParsedMessage struct {
	RawMessage []byte
	MessageID  string
	From       string
	To         []string
	CC         []string
	Subject    string
	Date       time.Time
	Body       string
	HTMLBody   string
	Headers    map[string]string

	// Nostr headers
	NostrPubkey  string
	NostrSig     string
	NostrHeaders []string
	IsEncrypted  bool
	Algorithm    string

	// References for threading
	InReplyTo  string
	References []string
}

// parseMessage parses a raw email message
func (p *InboundProcessor) parseMessage(data []byte) (*ParsedMessage, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse message: %w", err)
	}

	// Convert headers to map
	headers := make(map[string]string)
	for key := range msg.Header {
		headers[strings.ToLower(key)] = msg.Header.Get(key)
	}

	parsed := &ParsedMessage{
		RawMessage: data,
		MessageID:  msg.Header.Get("Message-ID"),
		Subject:    decodeHeader(msg.Header.Get("Subject")),
		InReplyTo:  msg.Header.Get("In-Reply-To"),
		Headers:    headers,
	}

	// Parse From
	if fromHeader := msg.Header.Get("From"); fromHeader != "" {
		if addr, err := mail.ParseAddress(fromHeader); err == nil {
			parsed.From = addr.Address
		} else {
			parsed.From = fromHeader
		}
	}

	// Parse To
	if toHeader := msg.Header.Get("To"); toHeader != "" {
		parsed.To = parseAddressList(toHeader)
	}

	// Parse CC
	if ccHeader := msg.Header.Get("Cc"); ccHeader != "" {
		parsed.CC = parseAddressList(ccHeader)
	}

	// Parse Date
	if dateHeader := msg.Header.Get("Date"); dateHeader != "" {
		if t, err := mail.ParseDate(dateHeader); err == nil {
			parsed.Date = t
		}
	}
	if parsed.Date.IsZero() {
		parsed.Date = time.Now()
	}

	// Parse References
	if refHeader := msg.Header.Get("References"); refHeader != "" {
		parsed.References = strings.Fields(refHeader)
	}

	// Parse Nostr headers
	parsed.NostrPubkey = msg.Header.Get("X-Nostr-Pubkey")
	parsed.NostrSig = msg.Header.Get("X-Nostr-Sig")
	if signedHeaders := msg.Header.Get("X-Nostr-Signed-Headers"); signedHeaders != "" {
		parsed.NostrHeaders = strings.Split(signedHeaders, ":")
	}

	// Check for encryption
	if algo := msg.Header.Get("X-Nostr-Encryption"); algo != "" {
		parsed.IsEncrypted = true
		parsed.Algorithm = algo
	}

	// Parse body
	if err := p.parseBody(msg, parsed); err != nil {
		p.logger.Warn("Failed to parse message body", zap.Error(err))
		// Try to use body as-is
		body, _ := io.ReadAll(msg.Body)
		parsed.Body = string(body)
	}

	return parsed, nil
}

// parseBody extracts plain text and HTML bodies from the message
func (p *InboundProcessor) parseBody(msg *mail.Message, parsed *ParsedMessage) error {
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// Assume plain text if content type is invalid
		body, _ := io.ReadAll(msg.Body)
		parsed.Body = string(body)
		return nil
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		return p.parseMultipart(msg.Body, params["boundary"], parsed)
	}

	// Simple message
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		return err
	}

	if strings.HasPrefix(mediaType, "text/html") {
		parsed.HTMLBody = string(body)
	} else {
		parsed.Body = string(body)
	}

	return nil
}

// parseMultipart parses a multipart message
func (p *InboundProcessor) parseMultipart(body io.Reader, boundary string, parsed *ParsedMessage) error {
	mr := multipart.NewReader(body, boundary)

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		contentType := part.Header.Get("Content-Type")
		mediaType, params, _ := mime.ParseMediaType(contentType)

		if strings.HasPrefix(mediaType, "multipart/") {
			// Nested multipart
			if err := p.parseMultipart(part, params["boundary"], parsed); err != nil {
				p.logger.Warn("Failed to parse nested multipart", zap.Error(err))
			}
			continue
		}

		data, err := io.ReadAll(part)
		if err != nil {
			continue
		}

		if strings.HasPrefix(mediaType, "text/plain") && parsed.Body == "" {
			parsed.Body = string(data)
		} else if strings.HasPrefix(mediaType, "text/html") && parsed.HTMLBody == "" {
			parsed.HTMLBody = string(data)
		}
		// TODO: Handle attachments
	}

	return nil
}

// storeForRecipient stores the message for a specific recipient
func (p *InboundProcessor) storeForRecipient(ctx context.Context, parsed *ParsedMessage, recipient string, verifyResult *VerificationResult) error {
	// Look up the recipient user
	user, err := p.db.GetUserByEmail(ctx, recipient)
	if err != nil {
		return fmt.Errorf("failed to lookup user: %w", err)
	}
	if user == nil {
		return fmt.Errorf("user not found: %s", recipient)
	}

	// Build the email record
	email := &storage.Email{
		UserID:      user.ID,
		FromAddress: parsed.From,
		ToAddress:   recipient,
		Subject:     parsed.Subject,
		Body:        parsed.Body,
		Direction:   "received",
		Status:      "active",
		Folder:      "INBOX",
	}

	if parsed.MessageID != "" {
		email.MessageID = &parsed.MessageID
	}

	if parsed.HTMLBody != "" {
		email.HTMLBody = &parsed.HTMLBody
	}

	if len(parsed.CC) > 0 {
		cc := strings.Join(parsed.CC, ", ")
		email.CC = &cc
	}

	if parsed.NostrPubkey != "" {
		email.SenderNpub = &parsed.NostrPubkey
	}

	if parsed.IsEncrypted {
		email.IsEncrypted = true
	}

	// Set verification status
	if verifyResult != nil {
		email.NostrVerified = verifyResult.Valid
		if verifyResult.Reason != "" {
			email.NostrVerificationError = &verifyResult.Reason
		}
		if verifyResult.Valid {
			now := time.Now()
			email.NostrVerifiedAt = &now
		}
	}

	// Create the email
	if err := p.db.CreateEmail(ctx, email); err != nil {
		return fmt.Errorf("failed to store email: %w", err)
	}

	p.logger.Info("Stored inbound email",
		zap.String("id", email.ID),
		zap.String("from", parsed.From),
		zap.String("to", recipient),
		zap.String("subject", parsed.Subject),
		zap.Bool("encrypted", parsed.IsEncrypted),
		zap.Bool("nostr_verified", email.NostrVerified))

	return nil
}

// parseAddressList parses a comma-separated list of email addresses
func parseAddressList(header string) []string {
	addrs, err := mail.ParseAddressList(header)
	if err != nil {
		// Return the raw header split by comma
		var result []string
		for _, part := range strings.Split(header, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				result = append(result, part)
			}
		}
		return result
	}

	result := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		result = append(result, addr.Address)
	}
	return result
}

// decodeHeader decodes RFC 2047 encoded headers
func decodeHeader(header string) string {
	dec := new(mime.WordDecoder)
	decoded, err := dec.DecodeHeader(header)
	if err != nil {
		return header
	}
	return decoded
}
