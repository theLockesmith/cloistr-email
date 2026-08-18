package api

import (
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strconv"

	"git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/email"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/identity"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

// EmailHandler handles email-related API endpoints
// This uses the full email service with transport, encryption, and identity
type EmailHandler struct {
	emailSvc *email.Service
	logger   *zap.Logger
}

// NewEmailHandler creates a new email handler
func NewEmailHandler(emailSvc *email.Service, logger *zap.Logger) *EmailHandler {
	return &EmailHandler{
		emailSvc: emailSvc,
		logger:   logger,
	}
}

func (h *EmailHandler) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}


// SendEmailV2 sends an email with full encryption and transport support
func (h *EmailHandler) SendEmailV2(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("SendEmailV2: processing request")

	// Get user's npub from context (set by auth middleware)
	userNpub := getUserID(r.Context())
	if userNpub == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	var req SendEmailRequestV2
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.BadRequest("INVALID_INPUT", "invalid request body").WriteResponse(w)
		return
	}

	// Validate required fields
	if len(req.To) == 0 {
		errors.BadRequest("VALIDATION_FAILED", "at least one recipient is required").WriteResponse(w)
		return
	}
	if req.Subject == "" {
		errors.BadRequest("VALIDATION_FAILED", "subject is required").WriteResponse(w)
		return
	}

	// Determine encryption mode
	var encMode encryption.EncryptionMode
	switch req.EncryptionMode {
	case EncryptionModeServer:
		encMode = encryption.ModeServerSide
		if req.Body == "" {
			errors.BadRequest("VALIDATION_FAILED", "body is required for server-side encryption").WriteResponse(w)
			return
		}
	case EncryptionModeClient:
		encMode = encryption.ModeClientSide
		if req.PreEncryptedBody == "" {
			errors.BadRequest("VALIDATION_FAILED", "pre_encrypted_body is required for client-side encryption").WriteResponse(w)
			return
		}
	case EncryptionModeNone, "":
		encMode = encryption.ModeNone
		if req.Body == "" {
			errors.BadRequest("VALIDATION_FAILED", "body is required").WriteResponse(w)
			return
		}
	default:
		errors.BadRequest("INVALID_INPUT", "invalid encryption_mode: must be 'none', 'server', or 'client'").WriteResponse(w)
		return
	}

	// Decode any attachments (base64 -> bytes).
	var attachments []email.AttachmentInput
	for _, a := range req.Attachments {
		data, err := base64.StdEncoding.DecodeString(a.DataBase64)
		if err != nil {
			errors.BadRequest("INVALID_INPUT", "attachment data_base64 is not valid base64: "+a.Filename).WriteResponse(w)
			return
		}
		attachments = append(attachments, email.AttachmentInput{
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Data:        data,
		})
	}

	// Build send request
	sendReq := &email.SendRequest{
		SenderNpub:       userNpub,
		From:             req.From,
		To:               req.To,
		CC:               req.CC,
		BCC:              req.BCC,
		Subject:          req.Subject,
		Body:             req.Body,
		HTMLBody:         req.HTMLBody,
		EncryptionMode:   encMode,
		PreEncryptedBody: req.PreEncryptedBody,
		RecipientPubkeys: req.RecipientPubkeys,
		InReplyTo:        req.InReplyTo,
		References:       req.References,
		Attachments:      attachments,
	}

	// Send the email
	result, err := h.emailSvc.Send(r.Context(), sendReq)
	if err != nil {
		h.logger.Error("Failed to send email", zap.Error(err))
		// Sender-eligibility failures are CLIENT errors (the user needs to
		// register/verify their @cloistr.xyz address or is sending from an
		// address they don't own) — return 4xx, not 500.
		var rateLimited *email.RateLimitError
		switch {
		case stderrors.Is(err, identity.ErrNoUnifiedAddress),
			stderrors.Is(err, identity.ErrAddressNotVerified):
			errors.Forbidden("SENDER_ADDRESS_REQUIRED", err.Error()).WriteResponse(w)
		case stderrors.Is(err, identity.ErrFromAddressMismatch),
			stderrors.Is(err, identity.ErrAddressOwnershipMismatch):
			errors.Forbidden("SENDER_ADDRESS_MISMATCH", err.Error()).WriteResponse(w)
		// Anonymous identities are receive-only — a permanent 403, not a retry.
		case stderrors.Is(err, email.ErrSendNotPermitted):
			errors.Forbidden("SEND_NOT_PERMITTED", err.Error()).WriteResponse(w)
		case stderrors.Is(err, email.ErrSendSuspended):
			errors.Forbidden("SEND_SUSPENDED", err.Error()).WriteResponse(w)
		// Rate limits are transient: 429 + Retry-After so clients back off
		// instead of hammering.
		case stderrors.As(err, &rateLimited):
			retry := int(rateLimited.RetryAfter.Seconds())
			if retry < 1 {
				retry = 1
			}
			errors.TooManyRequests(string(rateLimited.Reason), rateLimited.Error(), retry).WriteResponse(w)
		default:
			errors.InternalError("INTERNAL_ERROR", err.Error()).WriteResponse(w)
		}
		return
	}

	// Build response
	resp := SendEmailResponseV2{
		Status:         "sent",
		MessageID:      result.MessageID,
		EncryptionMode: req.EncryptionMode,
	}

	if !result.Success {
		resp.Status = "failed"
		resp.Error = result.Error
	}

	for _, r := range result.Recipients {
		resp.RecipientResults = append(resp.RecipientResults, RecipientSendResult{
			Email:     r.Email,
			Success:   r.Success,
			Encrypted: r.Encrypted,
			Error:     r.Error,
		})
	}

	h.logger.Info("Email sent via v2 endpoint",
		zap.Bool("success", result.Success),
		zap.String("message_id", result.MessageID),
		zap.Int("recipients", len(req.To)))

	h.respondJSON(w, http.StatusOK, resp)
}

// GetEmailV2 retrieves an email with decryption handling
func (h *EmailHandler) GetEmailV2(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("GetEmailV2: processing request")

	userNpub := getUserID(r.Context())
	if userNpub == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	vars := mux.Vars(r)
	emailID := vars["id"]
	if emailID == "" {
		errors.BadRequest("VALIDATION_FAILED", "email id is required").WriteResponse(w)
		return
	}

	result, err := h.emailSvc.GetEmail(r.Context(), userNpub, emailID)
	if err != nil {
		h.logger.Warn("Failed to get email", zap.Error(err), zap.String("email_id", emailID))
		errors.NotFound("RESOURCE_NOT_FOUND", "email not found").WriteResponse(w)
		return
	}

	resp := GetEmailResponseV2{
		ID:                       result.ID,
		From:                     result.From,
		To:                       []string{result.To},
		Subject:                  result.Subject,
		Body:                     result.Body,
		EncryptedBody:            result.EncryptedBody,
		IsEncrypted:              result.IsEncrypted,
		EncryptionMode:           result.EncryptionMode,
		RequiresClientDecryption: result.RequiresClientDecryption,
		SenderPubkey:             result.SenderPubkey,
		MessageID:                result.MessageID,
		Folder:                   result.Folder,
		CreatedAt:                result.CreatedAt.Format("2006-01-02T15:04:05Z"),
		NostrVerified:            result.NostrVerified,
	}

	if result.ReadAt != nil {
		resp.ReadAt = result.ReadAt.Format("2006-01-02T15:04:05Z")
	}
	if result.NostrVerifiedAt != nil {
		resp.NostrVerifiedAt = result.NostrVerifiedAt.Format("2006-01-02T15:04:05Z")
	}

	h.respondJSON(w, http.StatusOK, resp)
}

// ListEmailsV2 lists emails for the authenticated user
func (h *EmailHandler) ListEmailsV2(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("ListEmailsV2: processing request")

	userNpub := getUserID(r.Context())
	if userNpub == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	// Parse query parameters
	query := r.URL.Query()

	// Pagination
	page := 1
	limit := 50
	if p := query.Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if l := query.Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	// Filter
	filter := &storage.EmailFilter{
		Direction: query.Get("direction"),
		Status:    query.Get("status"),
		Folder:    query.Get("folder"),
		Search:    query.Get("search"),
	}

	opts := storage.ListOptions{
		Limit:  limit,
		Offset: (page - 1) * limit,
	}

	emails, total, err := h.emailSvc.ListEmails(r.Context(), userNpub, filter, opts)
	if err != nil {
		h.logger.Error("Failed to list emails", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to list emails").WriteResponse(w)
		return
	}

	// Build response
	emailResponses := make([]EmailResponse, 0, len(emails))
	for _, e := range emails {
		senderNpub := ""
		if e.SenderNpub != nil {
			senderNpub = *e.SenderNpub
		}

		resp := EmailResponse{
			ID:            e.ID,
			From:          e.FromAddress,
			To:            e.ToAddress,
			Subject:       e.Subject,
			IsEncrypted:   e.IsEncrypted,
			SenderNpub:    senderNpub,
			NostrVerified: e.NostrVerified,
			CreatedAt:     e.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
		if e.NostrVerifiedAt != nil {
			resp.NostrVerifiedAt = e.NostrVerifiedAt.Format("2006-01-02T15:04:05Z")
		}
		emailResponses = append(emailResponses, resp)
	}

	h.respondJSON(w, http.StatusOK, ListEmailsResponse{
		Emails: emailResponses,
		Total:  total,
		Page:   page,
		Limit:  limit,
	})
}

// DeleteEmailV2 soft-deletes an email
func (h *EmailHandler) DeleteEmailV2(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("DeleteEmailV2: processing request")

	userNpub := getUserID(r.Context())
	if userNpub == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	vars := mux.Vars(r)
	emailID := vars["id"]
	if emailID == "" {
		errors.BadRequest("VALIDATION_FAILED", "email id is required").WriteResponse(w)
		return
	}

	if err := h.emailSvc.DeleteEmail(r.Context(), userNpub, emailID); err != nil {
		h.logger.Warn("Failed to delete email", zap.Error(err), zap.String("email_id", emailID))
		errors.InternalError("INTERNAL_ERROR", "failed to delete email").WriteResponse(w)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// GetAttachmentV2 fetches and decrypts a single attachment from Blossom.
func (h *EmailHandler) GetAttachmentV2(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("GetAttachmentV2: processing request")

	userNpub := getUserID(r.Context())
	if userNpub == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	vars := mux.Vars(r)
	emailID := vars["id"]
	attachmentID := vars["attachmentId"]
	if emailID == "" || attachmentID == "" {
		errors.BadRequest("VALIDATION_FAILED", "email id and attachment id are required").WriteResponse(w)
		return
	}

	res, err := h.emailSvc.GetAttachment(r.Context(), userNpub, emailID, attachmentID)
	if err != nil {
		h.logger.Warn("Failed to get attachment", zap.Error(err),
			zap.String("email_id", emailID), zap.String("attachment_id", attachmentID))
		errors.NotFound("RESOURCE_NOT_FOUND", "attachment not found").WriteResponse(w)
		return
	}

	resp := AttachmentResponseV2{
		Filename:                 res.Filename,
		ContentType:              res.ContentType,
		RequiresClientDecryption: res.RequiresClientDecryption,
	}
	if res.RequiresClientDecryption {
		resp.Ciphertext = res.Ciphertext
	} else {
		resp.DataBase64 = base64.StdEncoding.EncodeToString(res.Data)
	}
	h.respondJSON(w, http.StatusOK, resp)
}
