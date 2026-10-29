package api

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/identity"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/ratelimit"
	"github.com/nbd-wtf/go-nostr/nip19"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-common/errors"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/auth"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/config"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/relays"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/storage"
	"github.com/google/uuid"
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
	recipients  RecipientResolver
	discoverRL  *ratelimit.Limiter
}

// SetDiscoverLimiter rate-limits key discovery per account.
func (h *Handler) SetDiscoverLimiter(l *ratelimit.Limiter) { h.discoverRL = l }

// RecipientResolver looks up a recipient's Nostr key: the identity service,
// the same lookup the send path uses (internal addresses, then NIP-05).
type RecipientResolver interface {
	ResolveRecipient(ctx context.Context, email string) (*identity.ExternalRecipient, error)
}

// SetRecipientResolver wires key discovery. The identity service is built
// after the Handler in main, hence a setter rather than a constructor option.
func (h *Handler) SetRecipientResolver(r RecipientResolver) { h.recipients = r }

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
		_ = json.NewEncoder(w).Encode(data)
	}
}

// AuthMiddleware validates session tokens and injects user context
func (h *Handler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract token from the Authorization header, or fall back to the
		// unified .cloistr.xyz signer session cookie (auto-sent to mail.cloistr.xyz).
		var token string
		if authHeader := r.Header.Get("Authorization"); authHeader != "" {
			// Expect "Bearer <token>" format
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				errors.Unauthorized("AUTH_INVALID", "invalid authorization header format").WriteResponse(w)
				return
			}
			token = parts[1]
		} else if c, err := r.Cookie("auth_token"); err == nil && c.Value != "" {
			token = c.Value
		} else {
			errors.Unauthorized("AUTH_REQUIRED", "missing authorization").WriteResponse(w)
			return
		}

		// Validate session
		session, err := h.auth.ValidateSession(r.Context(), token)
		if err != nil {
			h.logger.Debug("Session validation failed", zap.Error(err))
			errors.Unauthorized("AUTH_INVALID", "invalid or expired session").WriteResponse(w)
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
		errors.BadRequest("INVALID_INPUT", "invalid request body").WriteResponse(w)
		return
	}

	if req.BunkerURL == "" {
		errors.BadRequest("VALIDATION_FAILED", "bunker_url is required").WriteResponse(w)
		return
	}

	// Create auth challenge
	challenge, err := h.auth.CreateAuthChallenge(r.Context(), req.BunkerURL)
	if err != nil {
		h.logger.Error("Failed to create auth challenge", zap.Error(err))
		errors.BadRequest("VALIDATION_FAILED", err.Error()).WriteResponse(w)
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
		errors.BadRequest("INVALID_INPUT", "invalid request body").WriteResponse(w)
		return
	}

	if req.ChallengeID == "" || req.SignedEventJSON == "" {
		errors.BadRequest("VALIDATION_FAILED", "challenge_id and signed_event are required").WriteResponse(w)
		return
	}

	// Verify signature and create session
	session, err := h.auth.VerifyAuthSignature(r.Context(), req.ChallengeID, req.SignedEventJSON)
	if err != nil {
		h.logger.Warn("Auth verification failed", zap.Error(err))
		errors.Unauthorized("AUTH_INVALID", err.Error()).WriteResponse(w)
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
		errors.BadRequest("INVALID_INPUT", "invalid request body").WriteResponse(w)
		return
	}

	if req.ChallengeID == "" {
		errors.BadRequest("VALIDATION_FAILED", "challenge_id is required").WriteResponse(w)
		return
	}

	// Connect to bunker and get session
	session, err := h.auth.ConnectToBunker(r.Context(), req.ChallengeID)
	if err != nil {
		h.logger.Error("Bunker connection failed", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", err.Error()).WriteResponse(w)
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
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	if err := h.auth.Logout(r.Context(), session.ID); err != nil {
		h.logger.Error("Logout failed", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "logout failed").WriteResponse(w)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// GetSession returns the current session info
func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
	session := getSession(r.Context())
	if session == nil {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":    session.UserID,
		"email":      session.Email,
		"expires_at": session.ExpiresAt.Unix(),
	})
}

// ===== Email Endpoints =====

// EmailResponse represents an email in API responses (list view — no body content).
type EmailResponse struct {
	ID              string   `json:"id"`
	MessageID       string   `json:"message_id,omitempty"`
	InReplyTo       string   `json:"in_reply_to,omitempty"`
	From            string   `json:"from"`
	To              string   `json:"to"`
	CC              string   `json:"cc,omitempty"`
	Subject         string   `json:"subject"`
	IsEncrypted     bool     `json:"is_encrypted"`
	SenderNpub      string   `json:"sender_npub,omitempty"`
	NostrVerified   bool     `json:"nostr_verified"`
	NostrVerifiedAt string   `json:"nostr_verified_at,omitempty"`
	CreatedAt       string   `json:"created_at"`
	ReadAt          string   `json:"read_at,omitempty"`
	Folder          string   `json:"folder"`
	Labels          []string `json:"labels,omitempty"`
	IsStarred       bool     `json:"is_starred"`
	HasAttachments  bool     `json:"has_attachments"`
}

// ListEmailsResponse is the response for listing emails
type ListEmailsResponse struct {
	Emails []EmailResponse `json:"emails"`
	Total  int             `json:"total"`
	Page   int             `json:"page"`
	Limit  int             `json:"limit"`
}

// ===== Keys & Discovery Endpoints =====

// DiscoverKeyResponse is the response for key discovery.
//
// Pubkey is the 64-character lowercase hex key: it is what the UI reads and
// sends back in recipient_pubkeys, and what the send path compares against.
// Npub is the same key in bech32, for display only.
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
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	email := r.URL.Query().Get("email")
	if email == "" {
		errors.BadRequest("VALIDATION_FAILED", "email query parameter is required").WriteResponse(w)
		return
	}

	// Refuse a malformed address before it can become an outbound lookup.
	if err := identity.ValidateEmailFormat(email); err != nil {
		errors.BadRequest("INVALID_EMAIL", err.Error()).WriteResponse(w)
		return
	}
	if h.discoverRL != nil {
		d, err := h.discoverRL.Allow(r.Context(), ratelimit.Send{Pubkey: userID, Recipients: 1})
		if err != nil {
			// A limiter outage must not break compose; the lookup itself is
			// SSRF-safe, so failing open here costs only rate.
			h.logger.Warn("key discovery rate limit unavailable", zap.Error(err))
		} else if !d.Allowed {
			retry := int(d.RetryAfter.Seconds())
			if retry < 1 {
				retry = 1
			}
			errors.TooManyRequests(string(d.Reason), "too many key lookups; try again shortly", retry).WriteResponse(w)
			return
		}
	}

	resp := DiscoverKeyResponse{Email: email}
	if h.recipients != nil {
		rec, err := h.recipients.ResolveRecipient(r.Context(), email)
		if err != nil {
			// Not found, as far as the caller can act on it; the send path
			// resolves again and refuses a key it cannot confirm.
			h.logger.Warn("key discovery failed", zap.String("email", email), zap.Error(err))
		} else if rec != nil && rec.Npub != "" {
			resp.Found = true
			resp.Pubkey = strings.ToLower(rec.Npub)
			resp.Source = rec.DiscoveryMethod
			if npub, err := nip19.EncodePublicKey(resp.Pubkey); err == nil {
				resp.Npub = npub
			}
		}
	}
	h.respondJSON(w, http.StatusOK, resp)
}

// GetMyKey retrieves the authenticated user's Nostr key info
func (h *Handler) GetMyKey(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("Getting my key")

	userID := getUserID(r.Context())
	if userID == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	// Return BOTH forms. The UI shows an npub (that is what a user recognises
	// and pastes elsewhere), and previously read res.data.npub from a response
	// that only ever contained "pubkey" — so "View my public key" rendered
	// blank. Encoding here rather than in each client keeps one implementation.
	resp := map[string]string{"pubkey": userID}
	if npub, err := nip19.EncodePublicKey(userID); err == nil {
		resp["npub"] = npub
	} else {
		// A hex pubkey that will not encode is worth knowing about; the caller
		// still gets the hex form rather than an error.
		h.logger.Warn("Failed to encode pubkey as npub",
			zap.String("pubkey", userID), zap.Error(err))
	}
	h.respondJSON(w, http.StatusOK, resp)
}

// ===== Encryption Preference =====

// validEncryptionModes are the modes a user may choose as their send default.
// Anything else is rejected rather than stored, so a typo cannot silently
// disable encryption for every future message.
var validEncryptionModes = map[string]bool{"e2e": true, "server": true, "none": true}

// GetEncryptionCapabilities reports what encryption options are available to
// the caller: whether they have a live NIP-46 bunker connection (so the
// server can encrypt/decrypt on their behalf) and their stored preferred
// send mode.
//
// The Settings page has always called GET /api/v1/encryption/capabilities on
// load (ui/src/lib/api.ts, encryptionAPI.getCapabilities). No such route was
// ever registered — only /encryption/preferred-mode existed — so every page
// load 404'd (seen live as MAIL-7/36/37/38). The response shape
// (EncryptionCapabilityResponse) already existed in email_types.go; nothing
// constructed or served it.
func (h *Handler) GetEncryptionCapabilities(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r.Context())
	if userID == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	mode, err := h.db.GetPreferredEncryptionMode(r.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to read encryption preference", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to read encryption capabilities").WriteResponse(w)
		return
	}
	if mode == "" {
		mode = string(encryption.ModeNone)
	}

	// A live bunker connection is what lets the server encrypt/decrypt on the
	// user's behalf (see NIP46Status), so it governs both capability flags.
	hasNIP46 := h.auth.HasBunkerConnection(userID)

	resp := EncryptionCapabilityResponse{
		Npub:             userID,
		HasNIP46:         hasNIP46,
		PreferredMode:    encryption.EncryptionMode(mode),
		CanServerEncrypt: hasNIP46,
		CanServerDecrypt: hasNIP46,
	}
	if npub, err := nip19.EncodePublicKey(userID); err == nil {
		resp.Npub = npub
	} else {
		h.logger.Warn("Failed to encode pubkey as npub",
			zap.String("pubkey", userID), zap.Error(err))
	}

	h.respondJSON(w, http.StatusOK, resp)
}

// GetEncryptionPreference returns the caller's default encryption mode for new
// sends. An empty mode means no explicit preference.
func (h *Handler) GetEncryptionPreference(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r.Context())
	if userID == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}
	mode, err := h.db.GetPreferredEncryptionMode(r.Context(), userID)
	if err != nil {
		h.logger.Error("Failed to read encryption preference", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to read encryption preference").WriteResponse(w)
		return
	}
	h.respondJSON(w, http.StatusOK, map[string]string{"mode": mode})
}

// SetEncryptionPreference records the caller's default encryption mode.
//
// This endpoint did not exist. The Settings screen has always shown an
// encryption-mode control and posted it to /api/v1/encryption/preferred-mode,
// which 404'd silently, so the setting never applied and the user was never
// told. On a product that sells encryption, a control that pretends to save is
// worse than no control.
func (h *Handler) SetEncryptionPreference(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r.Context())
	if userID == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return
	}

	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errors.BadRequest("INVALID_REQUEST", "invalid request body").WriteResponse(w)
		return
	}
	if !validEncryptionModes[req.Mode] {
		errors.BadRequest("INVALID_MODE", "mode must be one of: e2e, server, none").WriteResponse(w)
		return
	}

	if err := h.db.SetPreferredEncryptionMode(r.Context(), userID, req.Mode); err != nil {
		h.logger.Error("Failed to save encryption preference", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to save encryption preference").WriteResponse(w)
		return
	}
	h.logger.Info("Saved encryption preference", zap.String("mode", req.Mode))
	h.respondJSON(w, http.StatusOK, map[string]string{"mode": req.Mode})
}

// ===== Contact Endpoints =====

// ContactStore is the address book storage the contact endpoints use. Every
// call is scoped to the signed-in mailbox.
type ContactStore interface {
	CreateContact(ctx context.Context, contact *storage.Contact) error
	GetContact(ctx context.Context, mailboxPubkey, id string) (*storage.Contact, error)
	ListContacts(ctx context.Context, mailboxPubkey string, opts storage.ListOptions) ([]*storage.Contact, int, error)
	SearchContacts(ctx context.Context, mailboxPubkey, query string, limit int) ([]*storage.Contact, error)
	UpdateContact(ctx context.Context, contact *storage.Contact) error
	DeleteContact(ctx context.Context, mailboxPubkey, id string) error
	EnsureMailbox(ctx context.Context, pubkey string) (*storage.Mailbox, error)
}

func (h *Handler) contactStore() ContactStore {
	if h.db == nil {
		return nil
	}
	return h.db
}

// ContactResponse represents a contact in API responses
type ContactResponse struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	Npub          string `json:"npub,omitempty"`
	Notes         string `json:"notes,omitempty"`
	Organization  string `json:"organization,omitempty"`
	Phone         string `json:"phone,omitempty"`
	AlwaysEncrypt bool   `json:"always_encrypt"`
	Blocked       bool   `json:"blocked"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func contactResponse(c *storage.Contact) ContactResponse {
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	return ContactResponse{
		ID:            c.ID,
		Email:         c.Email,
		Name:          deref(c.Name),
		Npub:          deref(c.Npub),
		Notes:         deref(c.Notes),
		Organization:  deref(c.Organization),
		Phone:         deref(c.Phone),
		AlwaysEncrypt: c.AlwaysEncrypt,
		Blocked:       c.Blocked,
		CreatedAt:     c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     c.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// contactFields are the editable fields. On add only email is required; on
// update an absent field is left as it was and "" clears an optional one.
type contactFields struct {
	Email         *string `json:"email"`
	Name          *string `json:"name"`
	Npub          *string `json:"npub"`
	Notes         *string `json:"notes"`
	Organization  *string `json:"organization"`
	Phone         *string `json:"phone"`
	AlwaysEncrypt *bool   `json:"always_encrypt"`
	Blocked       *bool   `json:"blocked"`
}

// AddContactRequest is the request for adding a contact
type AddContactRequest = contactFields

const maxContactField = 255

// apply validates f and copies the fields it carries onto c.
func (f contactFields) apply(c *storage.Contact) *errors.APIError {
	if f.Email != nil {
		addr, err := mail.ParseAddress(strings.TrimSpace(*f.Email))
		if err != nil || addr.Name != "" || len(addr.Address) > maxContactField {
			return errors.BadRequest("VALIDATION_FAILED", "email must be a plain email address")
		}
		c.Email = strings.ToLower(addr.Address)
	}
	opt := func(dst **string, src *string, field string, limit int) *errors.APIError {
		if src == nil {
			return nil
		}
		v := strings.TrimSpace(*src)
		if len(v) > limit {
			return errors.BadRequest("VALIDATION_FAILED", fmt.Sprintf("%s is too long", field))
		}
		if v == "" {
			*dst = nil
		} else {
			*dst = &v
		}
		return nil
	}
	for _, o := range []struct {
		dst   **string
		src   *string
		name  string
		limit int
	}{
		{&c.Name, f.Name, "name", maxContactField},
		{&c.Npub, f.Npub, "npub", maxContactField},
		{&c.Notes, f.Notes, "notes", 10000},
		{&c.Organization, f.Organization, "organization", maxContactField},
		{&c.Phone, f.Phone, "phone", 50},
	} {
		if err := opt(o.dst, o.src, o.name, o.limit); err != nil {
			return err
		}
	}
	if f.AlwaysEncrypt != nil {
		c.AlwaysEncrypt = *f.AlwaysEncrypt
	}
	if f.Blocked != nil {
		c.Blocked = *f.Blocked
	}
	return nil
}

// contactID returns the {id} path variable if it is a UUID. Anything else
// cannot name a contact, so it is answered as not found rather than handed
// to Postgres to fail as a 500.
func contactID(r *http.Request) (string, bool) {
	id := mux.Vars(r)["id"]
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}
	return id, true
}

// contactsReady answers 401/503 and returns false when the request cannot
// reach the address book.
func (h *Handler) contactsReady(w http.ResponseWriter, r *http.Request) (ContactStore, string, bool) {
	userID := getUserID(r.Context())
	if userID == "" {
		errors.Unauthorized("AUTH_REQUIRED", "not authenticated").WriteResponse(w)
		return nil, "", false
	}
	store := h.contactStore()
	if store == nil {
		errors.ServiceUnavailable("STORAGE_UNAVAILABLE", "contacts are unavailable", 30).WriteResponse(w)
		return nil, "", false
	}
	return store, userID, true
}

func (h *Handler) contactStorageError(w http.ResponseWriter, err error) {
	switch {
	case stderrors.Is(err, storage.ErrContactExists):
		errors.Conflict("CONTACT_EXISTS", "that address is already in your contacts").WriteResponse(w)
	case stderrors.Is(err, storage.ErrContactNotFound):
		errors.NotFound("RESOURCE_NOT_FOUND", "contact not found").WriteResponse(w)
	default:
		h.logger.Error("contact storage failed", zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "could not reach your contacts").WriteResponse(w)
	}
}

// ListContacts lists the signed-in user's contacts; ?search= filters them by
// name or email.
func (h *Handler) ListContacts(w http.ResponseWriter, r *http.Request) {
	store, userID, ok := h.contactsReady(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}

	var (
		contacts []*storage.Contact
		total    int
		err      error
	)
	if search := strings.TrimSpace(q.Get("search")); search != "" {
		contacts, err = store.SearchContacts(r.Context(), userID, search, limit)
		total = len(contacts)
	} else {
		contacts, total, err = store.ListContacts(r.Context(), userID, storage.ListOptions{Limit: limit, Offset: offset, OrderBy: "name"})
	}
	if err != nil {
		h.contactStorageError(w, err)
		return
	}
	out := make([]ContactResponse, 0, len(contacts))
	for _, c := range contacts {
		out = append(out, contactResponse(c))
	}
	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"contacts": out,
		"total":    total,
	})
}

// AddContact adds a contact to the signed-in user's address book.
func (h *Handler) AddContact(w http.ResponseWriter, r *http.Request) {
	store, userID, ok := h.contactsReady(w, r)
	if !ok {
		return
	}
	var req AddContactRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		errors.BadRequest("INVALID_INPUT", "invalid request body").WriteResponse(w)
		return
	}
	if req.Email == nil || strings.TrimSpace(*req.Email) == "" {
		errors.BadRequest("VALIDATION_FAILED", "email is required").WriteResponse(w)
		return
	}
	c := &storage.Contact{MailboxPubkey: userID}
	if apiErr := req.apply(c); apiErr != nil {
		apiErr.WriteResponse(w)
		return
	}
	// contacts.mailbox_pubkey references mailboxes; the session already proved
	// this pubkey, so its mailbox row may be created here.
	if _, err := store.EnsureMailbox(r.Context(), userID); err != nil {
		h.contactStorageError(w, err)
		return
	}
	if err := store.CreateContact(r.Context(), c); err != nil {
		h.contactStorageError(w, err)
		return
	}
	h.respondJSON(w, http.StatusCreated, contactResponse(c))
}

// GetContact retrieves one of the signed-in user's contacts.
func (h *Handler) GetContact(w http.ResponseWriter, r *http.Request) {
	store, userID, ok := h.contactsReady(w, r)
	if !ok {
		return
	}
	id, ok := contactID(r)
	if !ok {
		errors.NotFound("RESOURCE_NOT_FOUND", "contact not found").WriteResponse(w)
		return
	}
	c, err := store.GetContact(r.Context(), userID, id)
	if err != nil {
		h.contactStorageError(w, err)
		return
	}
	if c == nil {
		errors.NotFound("RESOURCE_NOT_FOUND", "contact not found").WriteResponse(w)
		return
	}
	h.respondJSON(w, http.StatusOK, contactResponse(c))
}

// UpdateContact changes the fields the request carries on one of the
// signed-in user's contacts.
func (h *Handler) UpdateContact(w http.ResponseWriter, r *http.Request) {
	store, userID, ok := h.contactsReady(w, r)
	if !ok {
		return
	}
	id, ok := contactID(r)
	if !ok {
		errors.NotFound("RESOURCE_NOT_FOUND", "contact not found").WriteResponse(w)
		return
	}
	var req contactFields
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		errors.BadRequest("INVALID_INPUT", "invalid request body").WriteResponse(w)
		return
	}
	c, err := store.GetContact(r.Context(), userID, id)
	if err != nil {
		h.contactStorageError(w, err)
		return
	}
	if c == nil {
		errors.NotFound("RESOURCE_NOT_FOUND", "contact not found").WriteResponse(w)
		return
	}
	if apiErr := req.apply(c); apiErr != nil {
		apiErr.WriteResponse(w)
		return
	}
	if err := store.UpdateContact(r.Context(), c); err != nil {
		h.contactStorageError(w, err)
		return
	}
	h.respondJSON(w, http.StatusOK, contactResponse(c))
}

// DeleteContact removes one of the signed-in user's contacts.
func (h *Handler) DeleteContact(w http.ResponseWriter, r *http.Request) {
	store, userID, ok := h.contactsReady(w, r)
	if !ok {
		return
	}
	id, ok := contactID(r)
	if !ok {
		errors.NotFound("RESOURCE_NOT_FOUND", "contact not found").WriteResponse(w)
		return
	}
	if err := store.DeleteContact(r.Context(), userID, id); err != nil {
		h.contactStorageError(w, err)
		return
	}
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
		errors.ServiceUnavailable("SERVICE_UNAVAILABLE", "relay preferences not configured", 30).WriteResponse(w)
		return
	}

	// Get pubkey from query param or use authenticated user
	pubkey := r.URL.Query().Get("pubkey")
	if pubkey == "" {
		userID := getUserID(r.Context())
		if userID == "" {
			errors.BadRequest("VALIDATION_FAILED", "pubkey query parameter required when not authenticated").WriteResponse(w)
			return
		}
		pubkey = userID
	}

	// Validate pubkey format (64 hex chars)
	if len(pubkey) != 64 {
		errors.BadRequest("INVALID_PUBKEY", "invalid pubkey format: expected 64 hex characters").WriteResponse(w)
		return
	}

	// Get relay preferences
	prefs, err := h.relayClient.GetRelayPrefs(r.Context(), pubkey)
	if err != nil {
		h.logger.Error("Failed to get relay preferences",
			zap.String("pubkey", pubkey[:16]+"..."),
			zap.Error(err))
		errors.InternalError("INTERNAL_ERROR", "failed to get relay preferences").WriteResponse(w)
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
