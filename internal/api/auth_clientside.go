package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/auth"
	"github.com/google/uuid"
	"github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// csGenHex returns n cryptographically random bytes encoded as lowercase hex.
func csGenHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ── request / response types ──────────────────────────────────────────────────

type startChallengeResponse struct {
	Challenge string `json:"challenge"`
	Nonce     string `json:"nonce"`
}

type verifyChallengeRequest struct {
	// SignedEvent is the full signed Nostr event object posted by the client.
	SignedEvent json.RawMessage `json:"signedEvent"`
}

// csEventContent is the JSON shape the client encodes into event.Content.
type csEventContent struct {
	Challenge string `json:"challenge"`
	Nonce     string `json:"nonce"`
}

// csUserInfo is the user sub-object expected by @cloistr/ui BackendAuthProvider.
type csUserInfo struct {
	Pubkey string `json:"pubkey"`
}

type csAuthResponse struct {
	// token and pubkey kept as-is for non-BackendAuthProvider callers.
	Token  string `json:"token"`
	Pubkey string `json:"pubkey"`

	// expires_at_unix is the raw unix timestamp; kept for any legacy callers.
	ExpiresAt int64 `json:"expires_at_unix"`

	// BackendAuthProvider-compatible fields (performBackendAuth / validateToken):
	// access_token mirrors token; expires_at is ISO-8601; user wraps pubkey.
	AccessToken string     `json:"access_token"`
	ExpiresAtISO string    `json:"expires_at"`
	User        csUserInfo `json:"user"`
}

type csRefreshResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at_unix"`

	// BackendAuthProvider-compatible aliases.
	AccessToken  string `json:"access_token"`
	ExpiresAtISO string `json:"expires_at"`
}

type csTokenInfoResponse struct {
	// Original fields.
	Pubkey    string `json:"pubkey,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Valid     bool   `json:"valid"`

	// BackendAuthProvider-compatible user sub-object (validateToken reads data.user).
	User *csUserInfo `json:"user,omitempty"`
}

// ── handlers ──────────────────────────────────────────────────────────────────

// StartChallenge handles GET /api/v1/auth/challenge.
//
// Issues a one-time challenge+nonce pair. The client signs a Nostr event whose
// content contains both values, then calls VerifyChallenge with the signed event.
// The nonce keys the challenge entry in Redis with a 5-minute TTL.
func (h *Handler) StartChallenge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	challenge, err := csGenHex(32) // 64-char hex
	if err != nil {
		h.logger.Error("StartChallenge: failed to generate challenge", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to generate challenge").WriteResponse(w)
		return
	}

	nonce, err := csGenHex(16) // 32-char hex
	if err != nil {
		h.logger.Error("StartChallenge: failed to generate nonce", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to generate nonce").WriteResponse(w)
		return
	}

	data := &auth.ChallengeData{
		Challenge: challenge,
		CreatedAt: time.Now().Unix(),
	}

	if err := h.sessions.SetNIP46Challenge(ctx, nonce, data, 5*time.Minute); err != nil {
		h.logger.Error("StartChallenge: failed to store challenge", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to create challenge").WriteResponse(w)
		return
	}

	h.logger.Debug("Client-side challenge issued")

	h.respondJSON(w, http.StatusOK, startChallengeResponse{
		Challenge: challenge,
		Nonce:     nonce,
	})
}

// VerifyChallenge handles POST /api/v1/auth/verify.
//
// Accepts { signedEvent } where signedEvent is a complete signed Nostr event
// (kind 27235) whose content is JSON.stringify({challenge, nonce}).
// Verifies: signature, kind, content, challenge binding, tag, and a ±5-minute
// replay guard on the event timestamp. On success mints a 24-hour session token
// and deletes the one-time challenge.
func (h *Handler) VerifyChallenge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req verifyChallengeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.BadRequest("INVALID_INPUT", "invalid request body").WriteResponse(w)
		return
	}
	if len(req.SignedEvent) == 0 {
		errors.BadRequest("VALIDATION_FAILED", "signedEvent is required").WriteResponse(w)
		return
	}

	// Decode the signed Nostr event.
	var event nostr.Event
	if err := json.Unmarshal(req.SignedEvent, &event); err != nil {
		errors.BadRequest("INVALID_INPUT", "invalid signedEvent JSON").WriteResponse(w)
		return
	}

	// (a) Verify Nostr signature.
	ok, err := event.CheckSignature()
	if err != nil || !ok {
		h.logger.Warn("VerifyChallenge: bad signature",
			zap.String("pubkey", event.PubKey),
			zap.Error(err))
		errors.Unauthorized("AUTH_INVALID", "invalid event signature").WriteResponse(w)
		return
	}

	// (b) Verify event kind.
	if event.Kind != 27235 {
		errors.Unauthorized("AUTH_INVALID", "unexpected event kind").WriteResponse(w)
		return
	}

	// (c) Parse event content as {challenge, nonce}.
	var content csEventContent
	if err := json.Unmarshal([]byte(event.Content), &content); err != nil {
		errors.Unauthorized("AUTH_INVALID", "invalid event content").WriteResponse(w)
		return
	}
	if content.Challenge == "" || content.Nonce == "" {
		errors.Unauthorized("AUTH_INVALID", "event content missing challenge or nonce").WriteResponse(w)
		return
	}

	// (d) Atomically consume the one-time challenge (Redis GETDEL). The signature
	// is already verified above, so consuming here is safe. Doing it atomically
	// means concurrent submissions of the same signed event mint AT MOST one
	// session (TOCTOU-free): the first caller receives the data, the rest get nil.
	challengeData, err := h.sessions.ConsumeNIP46Challenge(ctx, content.Nonce)
	if err != nil {
		h.logger.Error("VerifyChallenge: challenge consume error", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "challenge lookup failed").WriteResponse(w)
		return
	}
	if challengeData == nil {
		errors.Unauthorized("AUTH_INVALID", "challenge not found, expired, or already used").WriteResponse(w)
		return
	}

	// Stored challenge must match content.
	if challengeData.Challenge != content.Challenge {
		errors.Unauthorized("AUTH_INVALID", "challenge mismatch").WriteResponse(w)
		return
	}

	// ["challenge", <value>] tag must also match.
	var tagChallenge string
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == "challenge" {
			tagChallenge = tag[1]
			break
		}
	}
	if tagChallenge != content.Challenge {
		errors.Unauthorized("AUTH_INVALID", "challenge tag mismatch").WriteResponse(w)
		return
	}

	// (e) Replay guard: event timestamp must be within ±5 minutes of server time.
	diff := time.Now().Unix() - int64(event.CreatedAt)
	if diff < -300 || diff > 300 {
		errors.Unauthorized("AUTH_INVALID", "event timestamp out of acceptable range").WriteResponse(w)
		return
	}

	// Mint session token.
	token, err := csGenHex(32)
	if err != nil {
		h.logger.Error("VerifyChallenge: failed to generate token", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to mint session token").WriteResponse(w)
		return
	}

	now := time.Now()
	session := &auth.Session{
		ID:        uuid.New().String(),
		UserID:    event.PubKey,
		Token:     token,
		CreatedAt: now,
		ExpiresAt: now.Add(24 * time.Hour),
	}

	if err := h.sessions.SaveSession(ctx, session); err != nil {
		h.logger.Error("VerifyChallenge: failed to save session", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to create session").WriteResponse(w)
		return
	}

	// Challenge was already consumed atomically above (ConsumeNIP46Challenge),
	// so there is nothing left to invalidate here.

	h.logger.Info("Client-side auth successful", zap.String("pubkey", event.PubKey))

	h.respondJSON(w, http.StatusOK, csAuthResponse{
		Token:     token,
		ExpiresAt: session.ExpiresAt.Unix(),
		Pubkey:    event.PubKey,
		// BackendAuthProvider-compatible aliases:
		AccessToken:  token,
		ExpiresAtISO: session.ExpiresAt.UTC().Format(time.RFC3339),
		User:         csUserInfo{Pubkey: event.PubKey},
	})
}

// RefreshToken handles POST /api/v1/auth/refresh.
//
// Validates the current bearer token, rotates it, and extends the session by
// 24 hours. The old token is atomically invalidated: DeleteSession removes both
// the session-by-ID key and the old token index in Redis before the new session
// is persisted, so the old token cannot be accepted after this call.
func (h *Handler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract bearer token — mirror AuthMiddleware parsing.
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		errors.Unauthorized("AUTH_REQUIRED", "missing authorization header").WriteResponse(w)
		return
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		errors.Unauthorized("AUTH_INVALID", "invalid authorization header format").WriteResponse(w)
		return
	}
	oldToken := parts[1]

	session, err := h.auth.ValidateSession(ctx, oldToken)
	if err != nil {
		h.logger.Debug("RefreshToken: session validation failed", zap.Error(err))
		errors.Unauthorized("AUTH_INVALID", "invalid or expired session").WriteResponse(w)
		return
	}

	// Mint new token before touching storage.
	newToken, err := csGenHex(32)
	if err != nil {
		h.logger.Error("RefreshToken: failed to generate new token", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to mint new token").WriteResponse(w)
		return
	}

	// Delete old session: this removes both `session:<ID>` and
	// `session:token:<oldToken>` from Redis, so the old token is dead.
	if err := h.sessions.DeleteSession(ctx, session.ID); err != nil {
		h.logger.Error("RefreshToken: failed to delete old session", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "session refresh failed").WriteResponse(w)
		return
	}

	// Re-save session with rotated token and extended expiry.
	// SaveSession writes a fresh `session:token:<newToken>` index.
	session.Token = newToken
	session.ExpiresAt = time.Now().Add(24 * time.Hour)

	if err := h.sessions.SaveSession(ctx, session); err != nil {
		h.logger.Error("RefreshToken: failed to save refreshed session", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "session refresh failed").WriteResponse(w)
		return
	}

	h.logger.Debug("Token refreshed", zap.String("user_id", session.UserID))

	h.respondJSON(w, http.StatusOK, csRefreshResponse{
		Token:     newToken,
		ExpiresAt: session.ExpiresAt.Unix(),
		// BackendAuthProvider-compatible aliases:
		AccessToken:  newToken,
		ExpiresAtISO: session.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// TokenInfo handles GET /api/v1/auth/token-info.
//
// Returns token validity and metadata without requiring the AuthMiddleware.
// Responds {valid:false} with 401 on any failure so the caller never needs to
// distinguish between missing, malformed, or expired tokens.
func (h *Handler) TokenInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		h.respondJSON(w, http.StatusUnauthorized, csTokenInfoResponse{Valid: false})
		return
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		h.respondJSON(w, http.StatusUnauthorized, csTokenInfoResponse{Valid: false})
		return
	}
	token := parts[1]

	session, err := h.auth.ValidateSession(ctx, token)
	if err != nil {
		h.logger.Debug("TokenInfo: invalid token", zap.Error(err))
		h.respondJSON(w, http.StatusUnauthorized, csTokenInfoResponse{Valid: false})
		return
	}

	h.respondJSON(w, http.StatusOK, csTokenInfoResponse{
		Pubkey:    session.UserID,
		ExpiresAt: session.ExpiresAt.Unix(),
		Valid:     true,
		User:      &csUserInfo{Pubkey: session.UserID},
	})
}
