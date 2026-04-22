package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/auth"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/config"
	_ "git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption" // Will be used for email encryption
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/relays"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

// Context keys for request context
type contextKey string

const (
	contextKeySession contextKey = "session"
	contextKeyUserID  contextKey = "userID"
)

// Handler implements the API endpoints
type Handler struct {
	db          *storage.PostgreSQL
	auth        *auth.NIP46Handler
	sessions    auth.SessionStore
	config      *config.Config
	logger      *zap.Logger
	relayClient *relays.Client
}

// HandlerOption is a functional option for configuring the Handler
type HandlerOption func(*Handler)

// WithRelayClient sets the relay preferences client
func WithRelayClient(client *relays.Client) HandlerOption {
	return func(h *Handler) {
		h.relayClient = client
	}
}

// NewHandler creates a new API handler
func NewHandler(
	db *storage.PostgreSQL,
	nip46 *auth.NIP46Handler,
	sessions auth.SessionStore,
	cfg *config.Config,
	logger *zap.Logger,
	opts ...HandlerOption,
) *Handler {
	h := &Handler{
		db:       db,
		auth:     nip46,
		sessions: sessions,
		config:   cfg,
		logger:   logger,
	}

	for _, opt := range opts {
		opt(h)
	}

	return h
}

// Response helpers

func (h *Handler) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

func (h *Handler) respondError(w http.ResponseWriter, status int, message string) {
	h.respondJSON(w, status, map[string]string{"error": message})
}

// AuthMiddleware validates session tokens and injects user context
func (h *Handler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			h.respondError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}

		// Expect "Bearer <token>" format
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			h.respondError(w, http.StatusUnauthorized, "invalid authorization header format")
			return
		}

		token := parts[1]

		// Validate session
		session, err := h.auth.ValidateSession(r.Context(), token)
		if err != nil {
			h.logger.Debug("Session validation failed", zap.Error(err))
			h.respondError(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}

		// Add session and user ID to context
		ctx := context.WithValue(r.Context(), contextKeySession, session)
		ctx = context.WithValue(ctx, contextKeyUserID, session.UserID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// getSession extracts session from request context
func getSession(ctx context.Context) *auth.Session {
	session, _ := ctx.Value(contextKeySession).(*auth.Session)
	return session
}

// getUserID extracts user ID from request context
func getUserID(ctx context.Context) string {
	userID, _ := ctx.Value(contextKeyUserID).(string)
	return userID
}

// ===== Authentication Endpoints =====

// StartAuthRequest is the request body for starting auth
type StartAuthRequest struct {
	BunkerURL string `json:"bunker_url"`
}

// StartAuthResponse is the response for starting auth
type StartAuthResponse struct {
	ChallengeID  string `json:"challenge_id"`
	Challenge    string `json:"challenge"`
	BunkerPubkey string `json:"bunker_pubkey"`
	RelayURL     string `json:"relay_url"`
	ExpiresAt    int64  `json:"expires_at"`
}

// StartNIP46Auth initiates NIP-46 authentication
func (h *Handler) StartNIP46Auth(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Starting NIP-46 auth")

	var req StartAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.BunkerURL == "" {
		h.respondError(w, http.StatusBadRequest, "bunker_url is required")
		return
	}

	// Create auth challenge
	challenge, err := h.auth.CreateAuthChallenge(r.Context(), req.BunkerURL)
	if err != nil {
		h.logger.Error("Failed to create auth challenge", zap.Error(err))
		h.respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	h.respondJSON(w, http.StatusOK, StartAuthResponse{
		ChallengeID:  challenge.ID,
		Challenge:    challenge.Challenge,
		BunkerPubkey: challenge.BunkerPubkey,
		RelayURL:     challenge.RelayURL,
		ExpiresAt:    challenge.ExpiresAt,
	})
}

// VerifyAuthRequest is the request body for verifying auth
type VerifyAuthRequest struct {
	ChallengeID     string `json:"challenge_id"`
	SignedEventJSON string `json:"signed_event"`
}

// VerifyAuthResponse is the response for successful auth
type VerifyAuthResponse struct {
	Token     string `json:"token"`
	UserID    string `json:"user_id"`
	ExpiresAt int64  `json:"expires_at"`
}

// VerifyNIP46Auth verifies a NIP-46 signature and creates session
func (h *Handler) VerifyNIP46Auth(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Verifying NIP-46 auth")

	var req VerifyAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ChallengeID == "" || req.SignedEventJSON == "" {
		h.respondError(w, http.StatusBadRequest, "challenge_id and signed_event are required")
		return
	}

	// Verify signature and create session
	session, err := h.auth.VerifyAuthSignature(r.Context(), req.ChallengeID, req.SignedEventJSON)
	if err != nil {
		h.logger.Warn("Auth verification failed", zap.Error(err))
		h.respondError(w, http.StatusUnauthorized, err.Error())
		return
	}

	h.logger.Info("User authenticated successfully", zap.String("user_id", session.UserID))

	h.respondJSON(w, http.StatusOK, VerifyAuthResponse{
		Token:     session.Token,
		UserID:    session.UserID,
		ExpiresAt: session.ExpiresAt.Unix(),
	})
}

// ConnectBunkerRequest is the request to connect via bunker
type ConnectBunkerRequest struct {
	ChallengeID string `json:"challenge_id"`
}

// ConnectToBunker initiates the bunker connection flow
func (h *Handler) ConnectToBunker(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Connecting to bunker")

	var req ConnectBunkerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ChallengeID == "" {
		h.respondError(w, http.StatusBadRequest, "challenge_id is required")
		return
	}

	// Connect to bunker and get session
	session, err := h.auth.ConnectToBunker(r.Context(), req.ChallengeID)
	if err != nil {
		h.logger.Error("Bunker connection failed", zap.Error(err))
		h.respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.logger.Info("Connected to bunker successfully", zap.String("user_id", session.UserID))

	h.respondJSON(w, http.StatusOK, VerifyAuthResponse{
		Token:     session.Token,
		UserID:    session.UserID,
		ExpiresAt: session.ExpiresAt.Unix(),
	})
}

// Logout invalidates a session
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Logging out")

	session := getSession(r.Context())
	if session == nil {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	if err := h.auth.Logout(r.Context(), session.ID); err != nil {
		h.logger.Error("Logout failed", zap.Error(err))
		h.respondError(w, http.StatusInternalServerError, "logout failed")
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// GetSession returns the current session info
func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
	session := getSession(r.Context())
	if session == nil {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":    session.UserID,
		"email":      session.Email,
		"expires_at": session.ExpiresAt.Unix(),
	})
}

// ===== Email Endpoints =====

// EmailResponse represents an email in API responses
type EmailResponse struct {
	ID             string `json:"id"`
	From           string `json:"from"`
	To             string `json:"to"`
	Subject        string `json:"subject"`
	Body           string `json:"body"`
	IsEncrypted    bool   `json:"is_encrypted"`
	SenderNpub     string `json:"sender_npub,omitempty"`
	NostrVerified  bool   `json:"nostr_verified"`
	NostrVerifiedAt string `json:"nostr_verified_at,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// ListEmailsResponse is the response for listing emails
type ListEmailsResponse struct {
	Emails []EmailResponse `json:"emails"`
	Total  int             `json:"total"`
	Page   int             `json:"page"`
	Limit  int             `json:"limit"`
}

// ListEmails lists emails for the authenticated user
func (h *Handler) ListEmails(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Listing emails")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// TODO: Query database for emails
	// For now return empty list
	h.respondJSON(w, http.StatusOK, ListEmailsResponse{
		Emails: []EmailResponse{},
		Total:  0,
		Page:   1,
		Limit:  50,
	})
}

// SendEmailRequest is the request body for sending email
type SendEmailRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Encrypt bool   `json:"encrypt"`
}

// SendEmail sends an encrypted or plain email
func (h *Handler) SendEmail(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Sending email")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req SendEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.To == "" || req.Subject == "" {
		h.respondError(w, http.StatusBadRequest, "to and subject are required")
		return
	}

	// TODO: Implement email sending
	// 1. If encrypt=true, lookup recipient's npub
	// 2. Encrypt body with NIP-44 via bunker
	// 3. Submit to Stalwart
	// 4. Save to database

	h.logger.Info("Email sent",
		zap.String("to", req.To),
		zap.Bool("encrypted", req.Encrypt))

	h.respondJSON(w, http.StatusOK, map[string]string{
		"status":  "sent",
		"message": "Email queued for delivery",
	})
}

// GetEmail retrieves a single email by ID
func (h *Handler) GetEmail(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Getting email")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	vars := mux.Vars(r)
	emailID := vars["id"]
	if emailID == "" {
		h.respondError(w, http.StatusBadRequest, "email id is required")
		return
	}

	// TODO: Query database and decrypt if needed
	h.respondError(w, http.StatusNotFound, "email not found")
}

// ReplyEmail sends a reply to an email
func (h *Handler) ReplyEmail(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Replying to email")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	vars := mux.Vars(r)
	emailID := vars["id"]
	if emailID == "" {
		h.respondError(w, http.StatusBadRequest, "email id is required")
		return
	}

	// TODO: Get original email, compose reply, send
	h.respondJSON(w, http.StatusOK, map[string]string{"status": "replied"})
}

// DeleteEmail deletes an email
func (h *Handler) DeleteEmail(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Deleting email")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	vars := mux.Vars(r)
	emailID := vars["id"]
	if emailID == "" {
		h.respondError(w, http.StatusBadRequest, "email id is required")
		return
	}

	// TODO: Delete from database
	h.respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ===== Keys & Discovery Endpoints =====

// DiscoverKeyResponse is the response for key discovery
type DiscoverKeyResponse struct {
	Email  string `json:"email"`
	Npub   string `json:"npub,omitempty"`
	Pubkey string `json:"pubkey,omitempty"`
	Found  bool   `json:"found"`
	Source string `json:"source,omitempty"` // "contacts", "nip05", "manual"
}

// DiscoverKey looks up a recipient's Nostr public key for encryption
func (h *Handler) DiscoverKey(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Discovering key")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	email := r.URL.Query().Get("email")
	if email == "" {
		h.respondError(w, http.StatusBadRequest, "email query parameter is required")
		return
	}

	// TODO: Implement key discovery
	// 1. Check contacts database
	// 2. Try NIP-05 lookup
	// 3. Return result

	h.respondJSON(w, http.StatusOK, DiscoverKeyResponse{
		Email: email,
		Found: false,
	})
}

// ImportKeyRequest is the request for importing a key
type ImportKeyRequest struct {
	Email  string `json:"email"`
	Npub   string `json:"npub"`
	Pubkey string `json:"pubkey"`
}

// ImportKey imports a contact's Nostr key
func (h *Handler) ImportKey(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Importing key")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req ImportKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Email == "" || (req.Npub == "" && req.Pubkey == "") {
		h.respondError(w, http.StatusBadRequest, "email and npub/pubkey are required")
		return
	}

	// TODO: Validate and save key
	h.respondJSON(w, http.StatusOK, map[string]string{"status": "imported"})
}

// GetMyKey retrieves the authenticated user's Nostr key info
func (h *Handler) GetMyKey(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Getting my key")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]string{
		"pubkey": userID,
	})
}

// ===== Contact Endpoints =====

// ContactResponse represents a contact in API responses
type ContactResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Npub      string `json:"npub,omitempty"`
	CreatedAt string `json:"created_at"`
}

// ListContacts lists contacts for the authenticated user
func (h *Handler) ListContacts(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Listing contacts")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// TODO: Query database
	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"contacts": []ContactResponse{},
		"total":    0,
	})
}

// AddContactRequest is the request for adding a contact
type AddContactRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Npub  string `json:"npub"`
}

// AddContact adds a new contact
func (h *Handler) AddContact(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Adding contact")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req AddContactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Email == "" {
		h.respondError(w, http.StatusBadRequest, "email is required")
		return
	}

	// TODO: Save to database
	h.respondJSON(w, http.StatusCreated, map[string]string{
		"status": "created",
		"id":     "new-contact-id",
	})
}

// GetContact retrieves a single contact
func (h *Handler) GetContact(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Getting contact")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	vars := mux.Vars(r)
	contactID := vars["id"]
	if contactID == "" {
		h.respondError(w, http.StatusBadRequest, "contact id is required")
		return
	}

	// TODO: Query database
	h.respondError(w, http.StatusNotFound, "contact not found")
}

// UpdateContact updates a contact
func (h *Handler) UpdateContact(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Updating contact")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	vars := mux.Vars(r)
	contactID := vars["id"]
	if contactID == "" {
		h.respondError(w, http.StatusBadRequest, "contact id is required")
		return
	}

	// TODO: Update in database
	h.respondJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// DeleteContact deletes a contact
func (h *Handler) DeleteContact(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Deleting contact")

	userID := getUserID(r.Context())
	if userID == "" {
		h.respondError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	vars := mux.Vars(r)
	contactID := vars["id"]
	if contactID == "" {
		h.respondError(w, http.StatusBadRequest, "contact id is required")
		return
	}

	// TODO: Delete from database
	h.respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ===== Health Endpoints =====

// Health returns service health status
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	h.respondJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

// Ready returns service readiness status
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	// Check dependencies
	ctx := r.Context()

	// Check Redis
	if err := h.sessions.(*storage.RedisSessionStore).Health(ctx); err != nil {
		h.respondJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not ready",
			"error":  "redis unavailable",
		})
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// ===== Relay Preferences Endpoints =====

// RelayPrefsResponse represents relay preferences in API responses
type RelayPrefsResponse struct {
	Pubkey string        `json:"pubkey"`
	Source string        `json:"source"`
	Relays []RelayConfig `json:"relays"`
}

// RelayConfig represents a single relay configuration
type RelayConfig struct {
	URL   string `json:"url"`
	Read  bool   `json:"read"`
	Write bool   `json:"write"`
}

// GetRelayPrefs retrieves relay preferences for the authenticated user or a specified pubkey
func (h *Handler) GetRelayPrefs(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Getting relay preferences")

	if h.relayClient == nil {
		h.respondError(w, http.StatusServiceUnavailable, "relay preferences not configured")
		return
	}

	// Get pubkey from query param or use authenticated user
	pubkey := r.URL.Query().Get("pubkey")
	if pubkey == "" {
		userID := getUserID(r.Context())
		if userID == "" {
			h.respondError(w, http.StatusBadRequest, "pubkey query parameter required when not authenticated")
			return
		}
		pubkey = userID
	}

	// Validate pubkey format (64 hex chars)
	if len(pubkey) != 64 {
		h.respondError(w, http.StatusBadRequest, "invalid pubkey format: expected 64 hex characters")
		return
	}

	// Get relay preferences
	prefs, err := h.relayClient.GetRelayPrefs(r.Context(), pubkey)
	if err != nil {
		h.logger.Error("Failed to get relay preferences",
			zap.String("pubkey", pubkey[:16]+"..."),
			zap.Error(err))
		h.respondError(w, http.StatusInternalServerError, "failed to get relay preferences")
		return
	}

	// Convert to response format
	relayConfigs := make([]RelayConfig, len(prefs.Relays))
	for i, r := range prefs.Relays {
		relayConfigs[i] = RelayConfig{
			URL:   r.URL,
			Read:  r.Read,
			Write: r.Write,
		}
	}

	h.respondJSON(w, http.StatusOK, RelayPrefsResponse{
		Pubkey: prefs.Pubkey,
		Source: prefs.Source,
		Relays: relayConfigs,
	})
}
