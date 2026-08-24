// Package email provides email processing functionality.
package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/transport"
	"go.uber.org/zap"
)

// InboundProcessor handles incoming email messages
type InboundProcessor struct {
	db       *storage.PostgreSQL
	verifier *EmailVerifier
	logger   *zap.Logger
	uploader AttachmentUploader
}

// AttachmentUploader offloads inbound attachment bytes to blob storage.
//
// Injected rather than constructed here because the processor has no signer:
// Blossom uploads are authenticated per-user, and only the encryption service
// can sign on a bunker user's behalf. Optional — a nil uploader means the
// bytes cannot be stored, which is handled explicitly rather than by panicking.
type AttachmentUploader interface {
	// UploadFor stores data owned by recipientPubkey, returning its SHA-256 and
	// a retrieval URL.
	UploadFor(ctx context.Context, recipientPubkey string, data []byte, contentType string) (sha256 string, url string, err error)
}

// SetAttachmentUploader enables inbound attachment storage.
//
// A setter rather than a constructor parameter so existing callers and tests
// keep working, and so a deployment without Blossom configured simply stores
// attachment METADATA (see storeAttachments) instead of failing to build.
func (p *InboundProcessor) SetAttachmentUploader(u AttachmentUploader) { p.uploader = u }

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

// ValidateRecipient implements transport.RecipientValidator.
//
// Authority for "is this a deliverable address" is the shared addresses table
// (owned by cloistr-me), NOT any local record: a user who has picked a handle
// must be able to receive mail before they have ever logged in here. Any
// active alias resolves to its owner's single mailbox.
func (p *InboundProcessor) ValidateRecipient(ctx context.Context, address string) error {
	pubkey, err := p.resolveRecipientPubkey(ctx, address)
	if err != nil {
		return err
	}
	if pubkey == "" {
		return fmt.Errorf("no such recipient: %s", address)
	}
	return nil
}

// StripPlusTag removes a `+tag` suffix from the LOCAL part of an address.
//
//	alice+newsletter@cloistr.xyz -> alice@cloistr.xyz
//
// Plus addressing (RFC 5233 sub-addressing) is something users expect to just
// work — it is how people filter signups without handing out their real
// address. Without it every tagged address bounced as "no such recipient",
// which is worse than not offering the feature: the address looks accepted
// until mail to it silently fails.
//
// Two deliberate edge cases:
//
//   - The `+` must be in the local part, never the domain. `a@b+c.com` is a
//     (strange) domain, not a tag, so only the text before the LAST `@` is
//     considered.
//   - A local part that STARTS with `+` is not stripped. `+tag@domain` would
//     otherwise reduce to `@domain` — an empty mailbox name that must not be
//     allowed to match anything.
func StripPlusTag(address string) string {
	at := strings.LastIndex(address, "@")
	if at <= 0 {
		return address // no local part, or no @ at all — nothing to strip
	}
	local, domain := address[:at], address[at+1:]

	// A QUOTED local part means the `+` is literal, not a tag: `"a+b"@x` is a
	// mailbox actually named `a+b` (RFC 5322 quoted-string). Stripping there
	// would redirect mail to a different mailbox entirely.
	//
	// An unquoted `@` in the local part means the address is malformed. Leave it
	// untouched rather than rewriting it into a DIFFERENT malformed address —
	// it will not match a real account either way, and quietly reshaping a bad
	// input is how a lookup ends up hitting the wrong row.
	if strings.HasPrefix(local, `"`) || strings.Contains(local, "@") {
		return address
	}

	if plus := strings.Index(local, "+"); plus > 0 {
		local = local[:plus]
	}
	return local + "@" + domain
}

// resolveRecipientPubkey maps an inbound recipient address to the owning
// pubkey via the shared addresses table. Returns ("", nil) when the address
// is unknown or inactive.
//
// The EXACT address is tried first, then the plus-tag-stripped form. Order
// matters: someone who has literally registered `alice+work@cloistr.xyz` as a
// distinct address must keep receiving mail at it, rather than having it
// silently redirected to `alice@`.
func (p *InboundProcessor) resolveRecipientPubkey(ctx context.Context, address string) (string, error) {
	addr, err := p.db.GetAddressByEmail(ctx, address)
	if err != nil {
		return "", fmt.Errorf("recipient lookup failed: %w", err)
	}
	if addr != nil && addr.Active {
		return addr.Pubkey, nil
	}

	base := StripPlusTag(address)
	if base == address {
		return "", nil // no tag to fall back on
	}

	addr, err = p.db.GetAddressByEmail(ctx, base)
	if err != nil {
		return "", fmt.Errorf("recipient lookup failed: %w", err)
	}
	if addr == nil || !addr.Active {
		return "", nil
	}
	return addr.Pubkey, nil
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

	// Attachments extracted from the MIME tree, in the order encountered.
	Attachments []ParsedAttachment
}

// ParsedAttachment is one decoded non-body MIME part.
//
// Data is the DECODED bytes. Inbound attachments are almost always
// base64-encoded, so the raw part bytes are useless — storing them without
// decoding produces a file that is corrupt on download.
type ParsedAttachment struct {
	Filename    string
	ContentType string
	// ContentID backs `cid:` references in HTML bodies (inline images).
	ContentID string
	// Inline marks `Content-Disposition: inline` parts — real attachments to
	// store, but ones a client should render in place rather than list.
	Inline bool
	Data   []byte
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

		raw, err := io.ReadAll(part)
		if err != nil {
			continue
		}

		// Decode Content-Transfer-Encoding. This was previously skipped, so
		// every quoted-printable body was stored with literal `=20`/`=3D`
		// sequences in it and every base64 part was stored as base64 text.
		data := decodeTransferEncoding(raw, part.Header.Get("Content-Transfer-Encoding"))

		disposition, dispParams, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		filename := dispParams["filename"]
		if filename == "" {
			// Some senders omit the disposition filename and only set
			// Content-Type: application/pdf; name="x.pdf".
			filename = params["name"]
		}

		// A part is an attachment when it SAYS so, or when it is named, or when
		// it is not a body media type. Testing the media type alone is what made
		// an attached .txt file silently overwrite the message body.
		isAttachment := disposition == "attachment" || filename != "" ||
			(!strings.HasPrefix(mediaType, "text/plain") && !strings.HasPrefix(mediaType, "text/html"))

		if !isAttachment {
			if strings.HasPrefix(mediaType, "text/plain") && parsed.Body == "" {
				parsed.Body = string(data)
			} else if strings.HasPrefix(mediaType, "text/html") && parsed.HTMLBody == "" {
				parsed.HTMLBody = string(data)
			}
			continue
		}

		if filename == "" {
			// Unnamed attachments are legal (inline images usually are). Give it
			// a stable name so it is downloadable rather than discarded.
			filename = defaultAttachmentName(mediaType, len(parsed.Attachments)+1)
		}

		parsed.Attachments = append(parsed.Attachments, ParsedAttachment{
			Filename:    filename,
			ContentType: mediaType,
			ContentID:   strings.Trim(part.Header.Get("Content-ID"), "<>"),
			Inline:      disposition == "inline",
			Data:        data,
		})
	}

	return nil
}

// decodeTransferEncoding decodes a MIME part body per its
// Content-Transfer-Encoding header.
//
// Unknown or absent encodings (7bit, 8bit, binary) are returned untouched, and
// a DECODE FAILURE returns the raw bytes rather than an error: a body that is
// slightly wrong is better than a message that fails to deliver, and inbound
// mail from the open internet is routinely malformed.
func decodeTransferEncoding(raw []byte, encoding string) []byte {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		// Mail base64 is line-wrapped, and some senders pad incorrectly.
		// Stripping whitespace first makes the common cases decode.
		clean := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(raw))
		decoded, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			// Try the lenient raw-encoding variant before giving up.
			if d2, err2 := base64.RawStdEncoding.DecodeString(strings.TrimRight(clean, "=")); err2 == nil {
				return d2
			}
			return raw
		}
		return decoded
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if err != nil {
			return raw
		}
		return decoded
	default:
		return raw
	}
}

// defaultAttachmentName names a part that arrived without a filename.
func defaultAttachmentName(mediaType string, n int) string {
	ext := ""
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		ext = exts[0]
	}
	return fmt.Sprintf("attachment-%d%s", n, ext)
}

// storeForRecipient stores the message for a specific recipient
func (p *InboundProcessor) storeForRecipient(ctx context.Context, parsed *ParsedMessage, recipient string, verifyResult *VerificationResult) error {
	// Resolve the recipient address to its owning pubkey, then land the message
	// in that pubkey's single mailbox. Aliases converge here: every active
	// address for a pubkey delivers to the same mailbox.
	pubkey, err := p.resolveRecipientPubkey(ctx, recipient)
	if err != nil {
		return err
	}
	if pubkey == "" {
		return fmt.Errorf("no such recipient: %s", recipient)
	}

	// The mailbox is created on first delivery — the addresses table already
	// authorized this recipient, so no prior login is required.
	if _, err := p.db.EnsureMailbox(ctx, pubkey); err != nil {
		return fmt.Errorf("failed to ensure mailbox: %w", err)
	}

	// Build the email record
	email := &storage.Email{
		MailboxPubkey: pubkey,
		FromAddress:   parsed.From,
		ToAddress:     recipient,
		Subject:       parsed.Subject,
		Body:          parsed.Body,
		Direction:     "received",
		Status:        "active",
		Folder:        "INBOX",
	}

	if parsed.MessageID != "" {
		email.MessageID = &parsed.MessageID
	}
	if parsed.InReplyTo != "" {
		email.InReplyTo = &parsed.InReplyTo
	}
	if len(parsed.References) > 0 {
		refs := strings.Join(parsed.References, " ")
		email.References = &refs
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

	p.storeAttachments(ctx, email.ID, pubkey, parsed.Attachments)

	p.logger.Info("Stored inbound email",
		zap.String("id", email.ID),
		zap.String("from", parsed.From),
		zap.String("to", recipient),
		zap.String("subject", parsed.Subject),
		zap.Bool("encrypted", parsed.IsEncrypted),
		zap.Int("attachments", len(parsed.Attachments)),
		zap.Bool("nostr_verified", email.NostrVerified))

	return nil
}

// storeAttachments persists each parsed attachment against a stored email.
//
// Best-effort by design: the message itself is ALREADY committed, so returning
// an error here would fail a delivery that in fact succeeded, and the sending
// server would retry — duplicating the mail in the user's inbox.
//
// The row is written even when the blob upload fails or no uploader is
// configured. A visible attachment that cannot be downloaded is a bad outcome;
// an attachment the recipient is never told about is a worse one, because the
// user cannot know to ask the sender to resend.
func (p *InboundProcessor) storeAttachments(ctx context.Context, emailID, recipientPubkey string, atts []ParsedAttachment) {
	for _, att := range atts {
		size := int64(len(att.Data))
		contentType := att.ContentType
		record := &storage.Attachment{
			EmailID:     emailID,
			Filename:    att.Filename,
			ContentType: &contentType,
			SizeBytes:   &size,
		}

		if p.uploader == nil {
			p.logger.Warn("No attachment uploader configured; storing metadata only",
				zap.String("email_id", emailID), zap.String("filename", att.Filename))
		} else if sha, url, err := p.uploader.UploadFor(ctx, recipientPubkey, att.Data, att.ContentType); err != nil {
			p.logger.Warn("Failed to store inbound attachment blob",
				zap.String("email_id", emailID),
				zap.String("filename", att.Filename),
				zap.Error(err))
		} else {
			record.BlossomSHA256 = &sha
			if url != "" {
				record.BlossomURL = &url
			}
		}

		if err := p.db.CreateAttachment(ctx, record); err != nil {
			p.logger.Warn("Failed to record inbound attachment",
				zap.String("email_id", emailID),
				zap.String("filename", att.Filename),
				zap.Error(err))
		}
	}
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
