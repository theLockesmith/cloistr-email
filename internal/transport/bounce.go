// Package transport provides email transport mechanisms.
package transport

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
)

// BounceType represents the type of bounce
type BounceType string

const (
	// BounceTypeHard is a permanent failure (e.g., user doesn't exist)
	BounceTypeHard BounceType = "hard"

	// BounceTypeSoft is a temporary failure (e.g., mailbox full)
	BounceTypeSoft BounceType = "soft"

	// BounceTypeUnknown is an unknown bounce type
	BounceTypeUnknown BounceType = "unknown"
)

// BounceInfo contains information about a bounce
type BounceInfo struct {
	// Type is the bounce type
	Type BounceType

	// OriginalRecipient is the recipient that bounced
	OriginalRecipient string

	// OriginalMessageID is the Message-ID of the bounced message
	OriginalMessageID string

	// Reason is the bounce reason
	Reason string

	// DiagnosticCode is the SMTP diagnostic code (e.g., "550 5.1.1")
	DiagnosticCode string

	// RemoteServer is the server that generated the bounce
	RemoteServer string

	// ReceivedAt is when the bounce was received
	ReceivedAt time.Time

	// SenderPubkey attributes the bounce to the account that sent the original
	// message. Without it a bounce is only attributable to the recipient, which
	// makes per-account bounce rate — the primary abuse signal — impossible.
	// Empty when attribution fails; stored as NULL.
	SenderPubkey string
}

// SenderResolver maps a bounced message back to the pubkey of the account that
// sent it, using whatever identifying scraps the bounce carried.
type SenderResolver func(ctx context.Context, messageID, recipient string) (string, error)

// senderAttributionWindow bounds the recipient-based attribution fallback. Most
// bounces arrive within minutes; anything older is too likely to attribute to
// the wrong sender.
const senderAttributionWindow = 7 * 24 * time.Hour

// BounceHandler processes bounce messages
type BounceHandler struct {
	db     *sql.DB
	logger *zap.Logger

	// Callbacks
	onHardBounce func(ctx context.Context, bounce *BounceInfo) error
	onSoftBounce func(ctx context.Context, bounce *BounceInfo) error

	// resolveSender attributes an inbound bounce to a sending account
	resolveSender SenderResolver
}

// BounceHandlerOption configures the bounce handler
type BounceHandlerOption func(*BounceHandler)

// WithSenderResolver overrides how inbound bounces are attributed to a sending
// account. Defaults to QueueSenderResolver over the handler's own database.
func WithSenderResolver(r SenderResolver) BounceHandlerOption {
	return func(h *BounceHandler) {
		h.resolveSender = r
	}
}

// WithHardBounceCallback sets a callback for hard bounces
func WithHardBounceCallback(fn func(ctx context.Context, bounce *BounceInfo) error) BounceHandlerOption {
	return func(h *BounceHandler) {
		h.onHardBounce = fn
	}
}

// WithSoftBounceCallback sets a callback for soft bounces
func WithSoftBounceCallback(fn func(ctx context.Context, bounce *BounceInfo) error) BounceHandlerOption {
	return func(h *BounceHandler) {
		h.onSoftBounce = fn
	}
}

// RecordOutboundFailure records a bounce from an outbound delivery failure.
// This is called by the outbound queue when a message permanently fails.
//
// senderPubkey comes straight from the queue metadata, so this path needs no
// attribution guesswork — unlike inbound DSNs it always knows who sent the
// message. Pass "" only when the queue row genuinely carried no sender.
func (h *BounceHandler) RecordOutboundFailure(ctx context.Context, messageID, senderPubkey string, recipients []string, err error) error {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	// Classify the bounce type based on error message
	bounceType := h.classifyFromError(errStr)

	h.logger.Debug("Recording outbound failure as bounce",
		zap.String("message_id", messageID),
		zap.Strings("recipients", recipients),
		zap.String("bounce_type", string(bounceType)))

	// Record a bounce for each recipient
	for _, recipient := range recipients {
		bounce := &BounceInfo{
			Type:              bounceType,
			OriginalRecipient: recipient,
			OriginalMessageID: messageID,
			Reason:            errStr,
			DiagnosticCode:    extractSMTPCodeFromError(errStr),
			ReceivedAt:        time.Now(),
			SenderPubkey:      senderPubkey,
		}

		// Store in database if available
		if h.db != nil {
			if storeErr := h.storeBounce(ctx, bounce); storeErr != nil {
				h.logger.Error("Failed to store outbound failure bounce",
					zap.String("recipient", recipient),
					zap.Error(storeErr))
			}
		}

		// Call appropriate callback.
		//
		// Errors are logged, not returned: the bounce is already durably stored
		// above, and failing this whole call because a downstream notifier had a
		// bad day would lose that record. But they must not be DISCARDED — these
		// callbacks are what suppress future sends to a hard-bouncing address, so
		// a silent failure here means we keep mailing an address that already
		// bounced, which is precisely what damages sender reputation.
		switch bounceType {
		case BounceTypeHard:
			if h.onHardBounce != nil {
				if cbErr := h.onHardBounce(ctx, bounce); cbErr != nil {
					h.logger.Error("Hard-bounce callback failed",
						zap.String("message_id", messageID), zap.Error(cbErr))
				}
			}
		case BounceTypeSoft:
			if h.onSoftBounce != nil {
				if cbErr := h.onSoftBounce(ctx, bounce); cbErr != nil {
					h.logger.Error("Soft-bounce callback failed",
						zap.String("message_id", messageID), zap.Error(cbErr))
				}
			}
		}
	}

	return nil
}

// classifyFromError classifies a bounce type based on the error message
func (h *BounceHandler) classifyFromError(errStr string) BounceType {
	errLower := strings.ToLower(errStr)

	// Hard bounce indicators
	hardIndicators := []string{
		"user unknown", "no such user", "does not exist",
		"mailbox not found", "invalid recipient", "invalid address",
		"550 5.1.1", "550 5.1.2", "551", "553", "554",
		"address rejected", "recipient rejected",
	}

	for _, indicator := range hardIndicators {
		if strings.Contains(errLower, indicator) {
			return BounceTypeHard
		}
	}

	// Soft bounce indicators
	softIndicators := []string{
		"mailbox full", "over quota", "temporarily",
		"try again", "connection refused", "timeout",
		"connection reset", "no route to host",
		"421", "450", "451", "452",
	}

	for _, indicator := range softIndicators {
		if strings.Contains(errLower, indicator) {
			return BounceTypeSoft
		}
	}

	return BounceTypeUnknown
}

// extractSMTPCodeFromError extracts an SMTP status code from an error string
func extractSMTPCodeFromError(errStr string) string {
	// Look for patterns like "550", "5.1.1", "550 5.1.1"
	for i := 0; i < len(errStr)-2; i++ {
		if errStr[i] >= '4' && errStr[i] <= '5' {
			if errStr[i+1] >= '0' && errStr[i+1] <= '9' {
				if errStr[i+2] >= '0' && errStr[i+2] <= '9' {
					// Found a 3-digit code
					return errStr[i : i+3]
				}
			}
		}
	}
	return ""
}

// NewBounceHandler creates a new bounce handler
func NewBounceHandler(db *sql.DB, logger *zap.Logger, opts ...BounceHandlerOption) *BounceHandler {
	h := &BounceHandler{
		db:     db,
		logger: logger,
	}

	for _, opt := range opts {
		opt(h)
	}

	if h.resolveSender == nil {
		h.resolveSender = QueueSenderResolver(db)
	}

	return h
}

// QueueSenderResolver attributes a bounce to a sending account by looking the
// original message up in the outbound queue.
//
// The Message-ID path is exact. The recipient path is a fallback for the common
// case where a remote MTA returns a DSN with no usable Message-ID: it picks the
// most recent message this service sent to that recipient inside
// senderAttributionWindow. That can misattribute when two accounts mailed the
// same recipient in the same window, so the ladder treats bounce rate as one
// signal among several rather than grounds for action on its own.
func QueueSenderResolver(db *sql.DB) SenderResolver {
	return func(ctx context.Context, messageID, recipient string) (string, error) {
		if db == nil {
			return "", nil
		}

		if messageID != "" {
			const byMessageID = `
				SELECT metadata->>'sender_pubkey'
				FROM outbound_queue
				WHERE message_id = $1 AND metadata->>'sender_pubkey' IS NOT NULL
				ORDER BY created_at DESC
				LIMIT 1
			`
			var pubkey sql.NullString
			err := db.QueryRowContext(ctx, byMessageID, messageID).Scan(&pubkey)
			switch {
			case err == nil && pubkey.Valid && pubkey.String != "":
				return pubkey.String, nil
			case err != nil && !isBenignQueryErr(err):
				return "", err
			}
		}

		if recipient != "" {
			const byRecipient = `
				SELECT metadata->>'sender_pubkey'
				FROM outbound_queue
				WHERE recipients @> to_jsonb($1::text)
				  AND metadata->>'sender_pubkey' IS NOT NULL
				  AND created_at > $2
				ORDER BY created_at DESC
				LIMIT 1
			`
			var pubkey sql.NullString
			err := db.QueryRowContext(ctx, byRecipient, recipient,
				time.Now().Add(-senderAttributionWindow)).Scan(&pubkey)
			switch {
			case err == nil && pubkey.Valid:
				return pubkey.String, nil
			case err != nil && !isBenignQueryErr(err):
				return "", err
			}
		}

		return "", nil
	}
}

// isBenignQueryErr reports whether an error means "nothing to attribute" rather
// than a real failure — no matching row, or the optional table not existing.
func isBenignQueryErr(err error) bool {
	return err == sql.ErrNoRows || strings.Contains(err.Error(), "does not exist")
}

// attributeSender fills in info.SenderPubkey when it is not already known.
func (h *BounceHandler) attributeSender(ctx context.Context, info *BounceInfo) {
	if info.SenderPubkey != "" || h.resolveSender == nil {
		return
	}

	pubkey, err := h.resolveSender(ctx, info.OriginalMessageID, info.OriginalRecipient)
	if err != nil {
		h.logger.Warn("Failed to attribute bounce to a sender",
			zap.String("message_id", info.OriginalMessageID),
			zap.String("recipient", info.OriginalRecipient),
			zap.Error(err))
		return
	}
	if pubkey == "" {
		h.logger.Debug("Bounce could not be attributed to a sender",
			zap.String("message_id", info.OriginalMessageID),
			zap.String("recipient", info.OriginalRecipient))
		return
	}

	info.SenderPubkey = pubkey
}

// IsBounce checks if a message is a bounce message
func (h *BounceHandler) IsBounce(from string, data []byte) bool {
	// Check for empty envelope sender (standard for bounces per RFC 5321)
	if from == "" || from == "<>" {
		return true
	}

	// Parse the message to check for bounce indicators
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return false
	}

	// Check for common bounce indicators
	contentType := msg.Header.Get("Content-Type")
	if strings.Contains(contentType, "multipart/report") ||
		strings.Contains(contentType, "message/delivery-status") {
		return true
	}

	// Check subject for bounce indicators
	subject := strings.ToLower(msg.Header.Get("Subject"))
	bounceSubjects := []string{
		"delivery status notification",
		"delivery failure",
		"undeliverable",
		"mail delivery failed",
		"returned mail",
		"failure notice",
		"non-delivery",
	}

	for _, bounceSubject := range bounceSubjects {
		if strings.Contains(subject, bounceSubject) {
			return true
		}
	}

	return false
}

// ProcessBounce processes a bounce message
func (h *BounceHandler) ProcessBounce(ctx context.Context, from string, to []string, data []byte) error {
	h.logger.Debug("Processing bounce message",
		zap.String("from", from),
		zap.Strings("to", to))

	// Parse the bounce message
	bounceInfo, err := h.parseBounce(data)
	if err != nil {
		h.logger.Warn("Failed to parse bounce message", zap.Error(err))
		// Still process it as an unknown bounce
		bounceInfo = &BounceInfo{
			Type:       BounceTypeUnknown,
			Reason:     "failed to parse bounce",
			ReceivedAt: time.Now(),
		}
	}

	// Attribute the bounce to the account that sent the original message before
	// storing, so per-account bounce rate is queryable.
	h.attributeSender(ctx, bounceInfo)

	// Store the bounce in the database
	if err := h.storeBounce(ctx, bounceInfo); err != nil {
		h.logger.Error("Failed to store bounce", zap.Error(err))
	}

	// Call appropriate callback
	switch bounceInfo.Type {
	case BounceTypeHard:
		if h.onHardBounce != nil {
			if err := h.onHardBounce(ctx, bounceInfo); err != nil {
				h.logger.Error("Hard bounce callback failed", zap.Error(err))
			}
		}
	case BounceTypeSoft:
		if h.onSoftBounce != nil {
			if err := h.onSoftBounce(ctx, bounceInfo); err != nil {
				h.logger.Error("Soft bounce callback failed", zap.Error(err))
			}
		}
	}

	h.logger.Info("Bounce processed",
		zap.String("type", string(bounceInfo.Type)),
		zap.String("recipient", bounceInfo.OriginalRecipient),
		zap.String("reason", bounceInfo.Reason))

	return nil
}

// parseBounce extracts bounce information from a message
func (h *BounceHandler) parseBounce(data []byte) (*BounceInfo, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse message: %w", err)
	}

	info := &BounceInfo{
		ReceivedAt: time.Now(),
	}

	// A DSN keeps the interesting fields (Final-Recipient, Diagnostic-Code, the
	// returned copy of the original message) in MIME parts, not in the top-level
	// headers. Read the body once here and hand it to each extractor — msg.Body
	// is a one-shot reader, so they cannot each read it themselves.
	var body []byte
	if msg.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(msg.Body, maxDSNBodyScan))
	}

	// Try to extract the original Message-ID
	info.OriginalMessageID = extractOriginalMessageID(msg, body)

	// Try to extract the original recipient
	info.OriginalRecipient = extractOriginalRecipient(msg, body)

	// Parse the diagnostic code and determine bounce type
	info.DiagnosticCode, info.Reason = extractDiagnosticInfo(msg, body)
	info.Type = classifyBounce(info.DiagnosticCode, info.Reason)

	return info, nil
}

// embeddedMessageIDRe finds the original Message-ID inside the message/rfc822
// part that DSNs attach, which is where it lives when no header carries it.
var embeddedMessageIDRe = regexp.MustCompile(`(?im)^\s*Message-I[Dd]\s*:\s*(<[^>]+>)`)

// extractOriginalMessageID extracts the original Message-ID from a bounce.
// Accurate extraction matters beyond bookkeeping: it is the exact path for
// attributing the bounce to a sending account.
func extractOriginalMessageID(msg *mail.Message, body []byte) string {
	// Headers that carry the original Message-ID directly
	headers := []string{
		"X-Original-Message-ID",
		"In-Reply-To",
	}

	for _, header := range headers {
		if value := strings.TrimSpace(msg.Header.Get(header)); value != "" {
			return value
		}
	}

	// Try to find it in the References header
	if refs := msg.Header.Get("References"); refs != "" {
		// Return the first reference (usually the original message)
		parts := strings.Fields(refs)
		if len(parts) > 0 {
			return parts[0]
		}
	}

	// Fall back to the returned copy of the original message in the DSN body
	if m := embeddedMessageIDRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}

	return ""
}

// maxDSNBodyScan caps how much of a bounce body is scanned for the original
// Message-ID. DSN headers appear early; reading further just invites a large
// attachment to burn memory on every bounce.
const maxDSNBodyScan = 64 * 1024

// dsnFieldRe matches a named field in a message/delivery-status part, where the
// per-recipient DSN fields actually live rather than in the top-level headers.
func dsnFieldRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?im)^\s*` + regexp.QuoteMeta(name) + `\s*:\s*(.+)$`)
}

var (
	finalRecipientRe    = dsnFieldRe("Final-Recipient")
	originalRecipientRe = dsnFieldRe("Original-Recipient")
	diagnosticCodeRe    = dsnFieldRe("Diagnostic-Code")

	// diagnosticCodeParseRe splits "550 5.1.1 User unknown" into code, enhanced
	// status and reason.
	diagnosticCodeParseRe = regexp.MustCompile(`^(\d{3})\s+(\d\.\d\.\d)?\s*(.*)$`)
)

// dsnField returns a field from the top-level headers, falling back to the
// delivery-status part in the body.
func dsnField(msg *mail.Message, body []byte, header string, bodyRe *regexp.Regexp) string {
	if v := strings.TrimSpace(msg.Header.Get(header)); v != "" {
		return v
	}
	if m := bodyRe.FindSubmatch(body); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	return ""
}

// stripAddressType drops the "rfc822;" address-type prefix DSN fields carry.
func stripAddressType(value string) string {
	if parts := strings.SplitN(value, ";", 2); len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(value)
}

// extractOriginalRecipient extracts the original recipient from a bounce
func extractOriginalRecipient(msg *mail.Message, body []byte) string {
	// Check X-Failed-Recipients header
	if failed := msg.Header.Get("X-Failed-Recipients"); failed != "" {
		return strings.TrimSpace(failed)
	}

	// Check Original-Recipient (header or DSN part), format: rfc822;user@example.com
	if orig := dsnField(msg, body, "Original-Recipient", originalRecipientRe); orig != "" {
		return stripAddressType(orig)
	}

	// Try to extract from Final-Recipient
	if final := dsnField(msg, body, "Final-Recipient", finalRecipientRe); final != "" {
		return stripAddressType(final)
	}

	return ""
}

// extractDiagnosticInfo extracts diagnostic code and reason from a bounce
func extractDiagnosticInfo(msg *mail.Message, body []byte) (code string, reason string) {
	// Check Diagnostic-Code (header or DSN part)
	if diag := dsnField(msg, body, "Diagnostic-Code", diagnosticCodeRe); diag != "" {
		// Format: smtp;550 5.1.1 User unknown
		parts := strings.SplitN(diag, ";", 2)
		if len(parts) == 2 {
			code = strings.TrimSpace(parts[1])
			// Extract just the status code
			if matches := diagnosticCodeParseRe.FindStringSubmatch(code); len(matches) > 0 {
				reason = matches[3]
				code = matches[1]
				if matches[2] != "" {
					code = code + " " + matches[2]
				}
			}
		}
	}

	// Check Status header
	if status := msg.Header.Get("Status"); status != "" && code == "" {
		code = strings.TrimSpace(status)
	}

	// Try to extract from subject or body if still unknown
	if reason == "" {
		subject := msg.Header.Get("Subject")
		if strings.Contains(strings.ToLower(subject), "user unknown") ||
			strings.Contains(strings.ToLower(subject), "does not exist") {
			reason = "user unknown"
		} else if strings.Contains(strings.ToLower(subject), "mailbox full") {
			reason = "mailbox full"
		} else if strings.Contains(strings.ToLower(subject), "spam") ||
			strings.Contains(strings.ToLower(subject), "rejected") {
			reason = "message rejected"
		}
	}

	return code, reason
}

// classifyBounce determines the bounce type based on diagnostic info
func classifyBounce(code string, reason string) BounceType {
	// Check status code
	if strings.HasPrefix(code, "5") {
		// 5xx codes are permanent failures
		// But some are actually soft bounces
		if strings.HasPrefix(code, "5.2") || strings.HasPrefix(code, "5.7") {
			// 5.2.x = Mailbox issues (often temporary)
			// 5.7.x = Security/policy (might be temporary)
			if strings.Contains(strings.ToLower(reason), "full") ||
				strings.Contains(strings.ToLower(reason), "quota") {
				return BounceTypeSoft
			}
		}
		return BounceTypeHard
	}

	if strings.HasPrefix(code, "4") {
		// 4xx codes are temporary failures
		return BounceTypeSoft
	}

	// Check reason keywords
	reasonLower := strings.ToLower(reason)

	hardBounceKeywords := []string{
		"user unknown",
		"does not exist",
		"no such user",
		"invalid recipient",
		"unknown user",
		"mailbox not found",
		"address rejected",
	}

	for _, keyword := range hardBounceKeywords {
		if strings.Contains(reasonLower, keyword) {
			return BounceTypeHard
		}
	}

	softBounceKeywords := []string{
		"mailbox full",
		"over quota",
		"temporarily",
		"try again",
		"rate limit",
		"too many",
		"connection timeout",
	}

	for _, keyword := range softBounceKeywords {
		if strings.Contains(reasonLower, keyword) {
			return BounceTypeSoft
		}
	}

	return BounceTypeUnknown
}

// storeBounce stores bounce information in the database
func (h *BounceHandler) storeBounce(ctx context.Context, info *BounceInfo) error {
	if h.db == nil {
		return nil
	}

	query := `
		INSERT INTO email_bounces (
			original_recipient, original_message_id, bounce_type,
			reason, diagnostic_code, received_at, sender_pubkey
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	// NULL rather than "" for unattributed bounces, so the partial index on
	// sender_pubkey stays small and rate queries can't divide by phantom rows.
	var senderPubkey interface{}
	if info.SenderPubkey != "" {
		senderPubkey = info.SenderPubkey
	}

	_, err := h.db.ExecContext(ctx, query,
		info.OriginalRecipient,
		info.OriginalMessageID,
		info.Type,
		info.Reason,
		info.DiagnosticCode,
		info.ReceivedAt,
		senderPubkey,
	)

	// Ignore errors if table doesn't exist (optional feature)
	if err != nil && !strings.Contains(err.Error(), "does not exist") {
		return err
	}

	return nil
}

// GetBounceCount returns the number of bounces for a recipient
func (h *BounceHandler) GetBounceCount(ctx context.Context, recipient string, since time.Time) (int, error) {
	if h.db == nil {
		return 0, nil
	}

	query := `
		SELECT COUNT(*) FROM email_bounces
		WHERE original_recipient = $1 AND received_at > $2
	`

	var count int
	err := h.db.QueryRowContext(ctx, query, recipient, since).Scan(&count)
	if err != nil && !strings.Contains(err.Error(), "does not exist") {
		return 0, err
	}

	return count, nil
}

// SenderBounceCounts is the per-account bounce picture over some window.
type SenderBounceCounts struct {
	// Hard is the number of permanent failures
	Hard int

	// Soft is the number of temporary failures
	Soft int

	// Total counts every attributed bounce, including unknown-type ones
	Total int
}

// SenderBounceCounts returns how many bounces a sending account accrued since
// the given time. This is the raw input to the abuse ladder's bounce-rate rung;
// it deliberately returns counts rather than a rate, because the denominator
// (messages actually sent) lives in the outbound queue.
func (h *BounceHandler) SenderBounceCounts(ctx context.Context, senderPubkey string, since time.Time) (*SenderBounceCounts, error) {
	counts := &SenderBounceCounts{}
	if h.db == nil || senderPubkey == "" {
		return counts, nil
	}

	const query = `
		SELECT
			COUNT(*) FILTER (WHERE bounce_type = $3),
			COUNT(*) FILTER (WHERE bounce_type = $4),
			COUNT(*)
		FROM email_bounces
		WHERE sender_pubkey = $1 AND received_at > $2
	`

	err := h.db.QueryRowContext(ctx, query, senderPubkey, since, BounceTypeHard, BounceTypeSoft).
		Scan(&counts.Hard, &counts.Soft, &counts.Total)
	if err != nil && !isBenignQueryErr(err) {
		return counts, err
	}

	return counts, nil
}

// IsHardBounced checks if a recipient has hard bounced recently
func (h *BounceHandler) IsHardBounced(ctx context.Context, recipient string) (bool, error) {
	if h.db == nil {
		return false, nil
	}

	query := `
		SELECT COUNT(*) FROM email_bounces
		WHERE original_recipient = $1
		AND bounce_type = $2
		AND received_at > $3
	`

	// Consider hard bounces from the last 30 days
	since := time.Now().Add(-30 * 24 * time.Hour)

	var count int
	err := h.db.QueryRowContext(ctx, query, recipient, BounceTypeHard, since).Scan(&count)
	if err != nil && !strings.Contains(err.Error(), "does not exist") {
		return false, err
	}

	return count > 0, nil
}
