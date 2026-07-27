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

// Feedback-loop (FBL) ingestion.
//
// When a recipient at a large provider hits "report spam", enrolled senders get
// an Abuse Reporting Format report (RFC 5965) back. Complaint rate is a stronger
// abuse signal than bounce rate: a bounce means an address was wrong, a
// complaint means a real person did not want the mail. Providers start
// throttling around 0.1%, which is far below anything a bounce rate would flag.
//
// Ingestion only produces data once the sending domains are enrolled with each
// provider's FBL programme — that is an operator action, not a code path. Until
// then this parses and stores nothing because nothing arrives.

// ComplaintInfo is a single feedback-loop complaint.
type ComplaintInfo struct {
	// OriginalRecipient is the address that complained.
	OriginalRecipient string

	// OriginalMessageID identifies the reported message.
	OriginalMessageID string

	// FeedbackType is the ARF report type: "abuse", "fraud", "virus", "other".
	FeedbackType string

	// ReportingMTA is the provider that sent the report.
	ReportingMTA string

	// ReceivedAt is when the report was ingested.
	ReceivedAt time.Time

	// SenderPubkey attributes the complaint to the account that sent the
	// reported message. Empty when attribution fails; stored as NULL.
	SenderPubkey string
}

// FBLHandler ingests ARF complaint reports.
type FBLHandler struct {
	db     *sql.DB
	logger *zap.Logger

	resolveSender SenderResolver
}

// FBLHandlerOption configures the handler.
type FBLHandlerOption func(*FBLHandler)

// WithFBLSenderResolver overrides how complaints are attributed to an account.
func WithFBLSenderResolver(r SenderResolver) FBLHandlerOption {
	return func(h *FBLHandler) {
		h.resolveSender = r
	}
}

// NewFBLHandler creates a feedback-loop handler.
func NewFBLHandler(db *sql.DB, logger *zap.Logger, opts ...FBLHandlerOption) *FBLHandler {
	h := &FBLHandler{db: db, logger: logger}

	for _, opt := range opts {
		opt(h)
	}

	if h.resolveSender == nil {
		h.resolveSender = QueueSenderResolver(db)
	}

	return h
}

// IsComplaint reports whether a message is an ARF feedback report.
//
// The multipart/report content-type with report-type=feedback-report is the
// definitive marker (RFC 5965 §3); the body check catches providers that send a
// feedback report without setting the report-type parameter correctly.
func (h *FBLHandler) IsComplaint(data []byte) bool {
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return false
	}

	contentType := strings.ToLower(msg.Header.Get("Content-Type"))
	if strings.Contains(contentType, "report-type=feedback-report") {
		return true
	}
	if !strings.Contains(contentType, "multipart/report") {
		return false
	}

	body, err := io.ReadAll(io.LimitReader(msg.Body, maxDSNBodyScan))
	if err != nil {
		return false
	}
	return bytes.Contains(bytes.ToLower(body), []byte("message/feedback-report")) ||
		feedbackTypeRe.Match(body)
}

var (
	feedbackTypeRe   = dsnFieldRe("Feedback-Type")
	originalMailFrom = dsnFieldRe("Original-Mail-From")
	originalRcptTo   = dsnFieldRe("Original-Rcpt-To")
	reportingMTARe   = dsnFieldRe("Reporting-MTA")
	arfMessageIDRe   = dsnFieldRe("Message-ID")
)

// ProcessComplaint parses, attributes and stores a feedback report.
func (h *FBLHandler) ProcessComplaint(ctx context.Context, data []byte) error {
	info, err := h.parseComplaint(data)
	if err != nil {
		return fmt.Errorf("parse feedback report: %w", err)
	}

	h.attributeSender(ctx, info)

	if err := h.storeComplaint(ctx, info); err != nil {
		return fmt.Errorf("store feedback report: %w", err)
	}

	h.logger.Warn("Spam complaint received",
		zap.String("feedback_type", info.FeedbackType),
		zap.String("recipient", info.OriginalRecipient),
		zap.String("sender_pubkey", info.SenderPubkey),
		zap.String("reporting_mta", info.ReportingMTA))

	return nil
}

// parseComplaint extracts the ARF fields from a feedback report.
func (h *FBLHandler) parseComplaint(data []byte) (*ComplaintInfo, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse message: %w", err)
	}

	// As with DSNs, every field of interest lives in a MIME part rather than the
	// top-level headers, and msg.Body is a one-shot reader.
	var body []byte
	if msg.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(msg.Body, maxDSNBodyScan))
	}

	info := &ComplaintInfo{
		ReceivedAt:   time.Now(),
		FeedbackType: "other",
	}

	if m := feedbackTypeRe.FindSubmatch(body); m != nil {
		info.FeedbackType = strings.ToLower(strings.TrimSpace(string(m[1])))
	}
	if m := reportingMTARe.FindSubmatch(body); m != nil {
		info.ReportingMTA = stripAddressType(string(m[1]))
	}
	if m := originalRcptTo.FindSubmatch(body); m != nil {
		info.OriginalRecipient = stripAddressType(string(m[1]))
	}

	// The reported message's own Message-ID appears in the attached copy. Prefer
	// the last match: the report's own headers can appear first in the scanned
	// region, and attributing a complaint to the provider's report rather than
	// to the offending message would make it unattributable.
	info.OriginalMessageID = lastMatch(arfMessageIDRe, body)

	// Providers commonly redact the recipient. Original-Mail-From then gives the
	// envelope sender, which still narrows attribution to one of our domains.
	if info.OriginalRecipient == "" {
		if m := originalMailFrom.FindSubmatch(body); m != nil {
			h.logger.Debug("Feedback report redacted the recipient",
				zap.String("original_mail_from", stripAddressType(string(m[1]))))
		}
	}

	return info, nil
}

// lastMatch returns the final capture of re in body, or "".
func lastMatch(re *regexp.Regexp, body []byte) string {
	matches := re.FindAllSubmatch(body, -1)
	if len(matches) == 0 {
		return ""
	}
	return strings.TrimSpace(string(matches[len(matches)-1][1]))
}

// attributeSender fills in the sending account for a complaint.
func (h *FBLHandler) attributeSender(ctx context.Context, info *ComplaintInfo) {
	if h.resolveSender == nil {
		return
	}

	pubkey, err := h.resolveSender(ctx, info.OriginalMessageID, info.OriginalRecipient)
	if err != nil {
		h.logger.Warn("Failed to attribute complaint to a sender",
			zap.String("message_id", info.OriginalMessageID),
			zap.String("recipient", info.OriginalRecipient),
			zap.Error(err))
		return
	}
	if pubkey == "" {
		h.logger.Warn("Complaint could not be attributed to a sender",
			zap.String("message_id", info.OriginalMessageID),
			zap.String("recipient", info.OriginalRecipient))
		return
	}

	info.SenderPubkey = pubkey
}

// storeComplaint records the complaint for the abuse ladder to aggregate.
func (h *FBLHandler) storeComplaint(ctx context.Context, info *ComplaintInfo) error {
	if h.db == nil {
		return nil
	}

	const query = `
		INSERT INTO email_complaints (
			original_recipient, original_message_id, feedback_type,
			reporting_mta, received_at, sender_pubkey
		) VALUES ($1, $2, $3, $4, $5, $6)
	`

	var senderPubkey interface{}
	if info.SenderPubkey != "" {
		senderPubkey = info.SenderPubkey
	}

	_, err := h.db.ExecContext(ctx, query,
		info.OriginalRecipient,
		info.OriginalMessageID,
		info.FeedbackType,
		info.ReportingMTA,
		info.ReceivedAt,
		senderPubkey,
	)

	// A deployment that has not run migration 010 simply does not collect
	// complaints; that must not make inbound mail fail.
	if err != nil && !strings.Contains(err.Error(), "does not exist") {
		return err
	}

	return nil
}
