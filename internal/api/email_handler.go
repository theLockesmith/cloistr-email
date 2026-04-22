package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/email"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
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
		json.NewEncoder(w).Encode(data)
	}
}

func (h *EmailHandler) respondError(w http.ResponseWriter, status int, message string) {
	h.respondJSON(w, status, map[string]string{"error": message})
}

// SendEmailV2 sends an email with full encryption and transport support
func (h *EmailHandler) SendEmailV2(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("SendEmailV2: processing request")

	// Get user's npub from context (set by auth middleware)
	userNpub := getUserID(r.Context())
	if userNpub == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req SendEmailRequestV2
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate required fields
	if len(req.To) == 0 {
		h.respondError(w, http.StatusBadRequest, "at least one recipient is required")
		return
	}
	if req.Subject == "" {
		h.respondError(w, http.StatusBadRequest, "subject is required")
		return
	}

	// Determine encryption mode
	var encMode encryption.EncryptionMode
	switch req.EncryptionMode {
	case EncryptionModeServer:
		encMode = encryption.ModeServerSide
		if req.Body == "" {
			h.respondError(w, http.StatusBadRequest, "body is required for server-side encryption")
			return
		}
	case EncryptionModeClient:
		encMode = encryption.ModeClientSide
		if req.PreEncryptedBody == "" {
			h.respondError(w, http.StatusBadRequest, "pre_encrypted_body is required for client-side encryption")
			return
		}
	case EncryptionModeNone, "":
		encMode = encryption.ModeNone
		if req.Body == "" {
			h.respondError(w, http.StatusBadRequest, "body is required")
			return
		}
	default:
		h.respondError(w, http.StatusBadRequest, "invalid encryption_mode: must be 'none', 'server', or 'client'")
		return
	}

	// Build send request
	sendReq := &email.SendRequest{
		SenderNpub:       userNpub,
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
	}

	// Send the email
	result, err := h.emailSvc.Send(r.Context(), sendReq)
	if err != nil {
		h.logger.Error("Failed to send email", zap.Error(err))
		h.respondError(w, http.StatusInternalServerError, err.Error())
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
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	vars := mux.Vars(r)
	emailID := vars["id"]
	if emailID == "" {
		h.respondError(w, http.StatusBadRequest, "email id is required")
		return
	}

	result, err := h.emailSvc.GetEmail(r.Context(), userNpub, emailID)
	if err != nil {
		h.logger.Warn("Failed to get email", zap.Error(err), zap.String("email_id", emailID))
		h.respondError(w, http.StatusNotFound, "email not found")
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
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
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
		h.respondError(w, http.StatusInternalServerError, "failed to list emails")
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
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	vars := mux.Vars(r)
	emailID := vars["id"]
	if emailID == "" {
		h.respondError(w, http.StatusBadRequest, "email id is required")
		return
	}

	if err := h.emailSvc.DeleteEmail(r.Context(), userNpub, emailID); err != nil {
		h.logger.Warn("Failed to delete email", zap.Error(err), zap.String("email_id", emailID))
		h.respondError(w, http.StatusInternalServerError, "failed to delete email")
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
