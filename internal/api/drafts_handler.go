package api

import (
	"encoding/json"
	stderrors "errors"
	"net/http"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/email"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

// SaveDraftRequestV2 is the compose screen's draft. For an encrypted draft,
// body is the client body envelope (wrapped to the sender) and subject is
// ignored: the real subject is inside the envelope.
type SaveDraftRequestV2 struct {
	To             string   `json:"to"`
	CC             []string `json:"cc,omitempty"`
	Subject        string   `json:"subject"`
	Body           string   `json:"body"`
	EncryptionMode string   `json:"encryption_mode,omitempty"`
	InReplyTo      string   `json:"in_reply_to,omitempty"`
	References     []string `json:"references,omitempty"`
}

// SaveDraftResponseV2 identifies the saved draft.
type SaveDraftResponseV2 struct {
	ID        string    `json:"id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateDraftV2 handles POST /api/v2/email/drafts.
func (h *EmailHandler) CreateDraftV2(w http.ResponseWriter, r *http.Request) {
	h.saveDraft(w, r, "", http.StatusCreated)
}

// UpdateDraftV2 handles PUT /api/v2/email/drafts/{id}.
func (h *EmailHandler) UpdateDraftV2(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		errors.BadRequest("VALIDATION_FAILED", "draft id required").WriteResponse(w)
		return
	}
	h.saveDraft(w, r, id, http.StatusOK)
}

func (h *EmailHandler) saveDraft(w http.ResponseWriter, r *http.Request, id string, okStatus int) {
	userNpub := getUserID(r.Context())
	if userNpub == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	var req SaveDraftRequestV2
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*email.MaxDraftBodyBytes)).Decode(&req); err != nil {
		errors.BadRequest("INVALID_INPUT", "invalid request body").WriteResponse(w)
		return
	}

	d, err := h.emailSvc.SaveDraft(r.Context(), userNpub, &email.DraftInput{
		ID:             id,
		To:             req.To,
		CC:             req.CC,
		Subject:        req.Subject,
		Body:           req.Body,
		EncryptionMode: encryption.EncryptionMode(req.EncryptionMode),
		InReplyTo:      req.InReplyTo,
		References:     req.References,
	})
	if err != nil {
		switch {
		case stderrors.Is(err, email.ErrDraftNotFound):
			errors.New("DRAFT_NOT_FOUND", "draft not found", http.StatusNotFound).WriteResponse(w)
		case stderrors.Is(err, email.ErrClientBodyNotEncrypted):
			errors.BadRequest("BODY_NOT_ENCRYPTED", err.Error()).WriteResponse(w)
		case stderrors.Is(err, email.ErrDraftKeyNotOwner):
			errors.BadRequest("DRAFT_NOT_READABLE", err.Error()).WriteResponse(w)
		case stderrors.Is(err, email.ErrDraftServerMode):
			errors.BadRequest("DRAFT_SERVER_ENCRYPTION", err.Error()).WriteResponse(w)
		case stderrors.Is(err, email.ErrDraftTooLarge):
			errors.New("DRAFT_TOO_LARGE", err.Error(), http.StatusRequestEntityTooLarge).WriteResponse(w)
		default:
			h.logger.Error("Failed to save draft", zap.Error(err))
			errors.InternalError("INTERNAL_ERROR", "failed to save draft").WriteResponse(w)
		}
		return
	}
	h.respondJSON(w, okStatus, SaveDraftResponseV2{ID: d.ID, UpdatedAt: d.UpdatedAt})
}
