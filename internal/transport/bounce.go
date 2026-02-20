// Package transport provides email transport mechanisms.
package transport

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
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
}

// BounceHandler processes bounce messages
type BounceHandler struct {
	db     *sql.DB
	logger *zap.Logger

	// Callbacks
	onHardBounce func(ctx context.Context, bounce *BounceInfo) error
	onSoftBounce func(ctx context.Context, bounce *BounceInfo) error
}

// BounceHandlerOption configures the bounce handler
type BounceHandlerOption func(*BounceHandler)

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

// RecordOutboundFailure records a bounce from an outbound delivery failure
// This is called by the outbound queue when a message permanently fails
func (h *BounceHandler) RecordOutboundFailure(ctx context.Context, messageID string, recipients []string, err error) error {
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
		}

		// Store in database if available
		if h.db != nil {
			if storeErr := h.storeBounce(ctx, bounce); storeErr != nil {
				h.logger.Error("Failed to store outbound failure bounce",
					zap.String("recipient", recipient),
					zap.Error(storeErr))
			}
		}

		// Call appropriate callback
		switch bounceType {
		case BounceTypeHard:
			if h.onHardBounce != nil {
				h.onHardBounce(ctx, bounce)
			}
		case BounceTypeSoft:
			if h.onSoftBounce != nil {
				h.onSoftBounce(ctx, bounce)
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

	return h
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

	// Try to extract the original Message-ID
	info.OriginalMessageID = extractOriginalMessageID(msg)

	// Try to extract the original recipient
	info.OriginalRecipient = extractOriginalRecipient(msg)

	// Parse the diagnostic code and determine bounce type
	info.DiagnosticCode, info.Reason = extractDiagnosticInfo(msg)
	info.Type = classifyBounce(info.DiagnosticCode, info.Reason)

	return info, nil
}

// extractOriginalMessageID extracts the original Message-ID from a bounce
func extractOriginalMessageID(msg *mail.Message) string {
	// Check common headers for original Message-ID
	headers := []string{
		"X-Failed-Recipients",
		"X-Original-Message-ID",
	}

	for _, header := range headers {
		if value := msg.Header.Get(header); value != "" {
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

	return ""
}

// extractOriginalRecipient extracts the original recipient from a bounce
func extractOriginalRecipient(msg *mail.Message) string {
	// Check X-Failed-Recipients header
	if failed := msg.Header.Get("X-Failed-Recipients"); failed != "" {
		return strings.TrimSpace(failed)
	}

	// Check Original-Recipient header
	if orig := msg.Header.Get("Original-Recipient"); orig != "" {
		// Format: rfc822;user@example.com
		parts := strings.SplitN(orig, ";", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
		return strings.TrimSpace(orig)
	}

	// Try to extract from Final-Recipient
	if final := msg.Header.Get("Final-Recipient"); final != "" {
		parts := strings.SplitN(final, ";", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
	}

	return ""
}

// extractDiagnosticInfo extracts diagnostic code and reason from a bounce
func extractDiagnosticInfo(msg *mail.Message) (code string, reason string) {
	// Check Diagnostic-Code header
	if diag := msg.Header.Get("Diagnostic-Code"); diag != "" {
		// Format: smtp;550 5.1.1 User unknown
		parts := strings.SplitN(diag, ";", 2)
		if len(parts) == 2 {
			code = strings.TrimSpace(parts[1])
			// Extract just the status code
			codeMatch := regexp.MustCompile(`^(\d{3})\s+(\d\.\d\.\d)?\s*(.*)$`)
			if matches := codeMatch.FindStringSubmatch(code); len(matches) > 0 {
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
			reason, diagnostic_code, received_at
		) VALUES ($1, $2, $3, $4, $5, $6)
	`

	_, err := h.db.ExecContext(ctx, query,
		info.OriginalRecipient,
		info.OriginalMessageID,
		info.Type,
		info.Reason,
		info.DiagnosticCode,
		info.ReceivedAt,
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
