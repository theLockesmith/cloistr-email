package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nbd-wtf/go-nostr"
	"go.uber.org/zap"
)

// NIP46Handler manages NIP-46 authentication with nsecbunker
type NIP46Handler struct {
	relayURL       string
	sessionStore   SessionStore
	stalwartClient *StalwartClient
	logger         *zap.Logger
}

// SessionStore interface for managing sessions
type SessionStore interface {
	SaveSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	SetNIP46Challenge(ctx context.Context, challengeID string, challenge string, ttl time.Duration) error
	GetNIP46Challenge(ctx context.Context, challengeID string) (string, error)
}

// Session represents an authenticated session
type Session struct {
	ID        string
	UserID    string // Nostr npub
	Email     string
	Token     string
	ExpiresAt time.Time
}

// NewNIP46Handler creates a new NIP-46 auth handler
func NewNIP46Handler(
	relayURL string,
	sessionStore SessionStore,
	stalwartClient *StalwartClient,
	logger *zap.Logger,
) (*NIP46Handler, error) {
	logger.Info("Initializing NIP-46 auth handler", zap.String("relay_url", relayURL))

	return &NIP46Handler{
		relayURL:       relayURL,
		sessionStore:   sessionStore,
		stalwartClient: stalwartClient,
		logger:         logger,
	}, nil
}

// AuthChallenge represents a NIP-46 authentication challenge
type AuthChallenge struct {
	ID        string    `json:"id"`
	Challenge string    `json:"challenge"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CreateAuthChallenge creates a new NIP-46 authentication challenge
func (h *NIP46Handler) CreateAuthChallenge(ctx context.Context) (*AuthChallenge, error) {
	h.logger.Debug("Creating auth challenge")

	// Generate challenge ID and content
	id := uuid.New().String()
	challenge := uuid.New().String()

	now := time.Now()
	expiresAt := now.Add(5 * time.Minute)

	// Store challenge in Redis with TTL
	if err := h.sessionStore.SetNIP46Challenge(ctx, id, challenge, 5*time.Minute); err != nil {
		h.logger.Error("Failed to store challenge", zap.Error(err))
		return nil, err
	}

	return &AuthChallenge{
		ID:        id,
		Challenge: challenge,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}, nil
}

// VerifyAuthSignature verifies a signature for a NIP-46 challenge
// This is called after nsecbunker signs the challenge
func (h *NIP46Handler) VerifyAuthSignature(ctx context.Context, challengeID, signature string, pubkey string) (*Session, error) {
	h.logger.Debug("Verifying auth signature", zap.String("challenge_id", challengeID), zap.String("pubkey", pubkey))

	// Retrieve challenge from Redis
	challenge, err := h.sessionStore.GetNIP46Challenge(ctx, challengeID)
	if err != nil {
		h.logger.Error("Failed to retrieve challenge", zap.Error(err))
		return nil, fmt.Errorf("challenge retrieval failed: %w", err)
	}

	if challenge == "" {
		h.logger.Warn("Challenge not found or expired", zap.String("challenge_id", challengeID))
		return nil, fmt.Errorf("challenge not found or expired")
	}

	// Verify the signature (stub - actual implementation needs nostr signature verification)
	// This would verify that the signature is a valid Nostr event signed by pubkey
	h.logger.Debug("Signature verified", zap.String("pubkey", pubkey))

	// Create authenticated session
	sessionID := uuid.New().String()
	sessionToken := uuid.New().String()

	session := &Session{
		ID:        sessionID,
		UserID:    pubkey, // Store the npub as UserID
		Token:     sessionToken,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	// Save session
	if err := h.sessionStore.SaveSession(ctx, session); err != nil {
		h.logger.Error("Failed to save session", zap.Error(err))
		return nil, err
	}

	h.logger.Info("Authentication successful", zap.String("user_id", pubkey))

	return session, nil
}

// ValidateSession validates an existing session token
func (h *NIP46Handler) ValidateSession(ctx context.Context, token string) (*Session, error) {
	h.logger.Debug("Validating session token")

	// Stub: actual implementation would look up session from Redis by token
	// This would return the session if valid and not expired

	return nil, fmt.Errorf("invalid session token")
}

// SignEvent signs a Nostr event using NIP-46
// This requests signature from nsecbunker
func (h *NIP46Handler) SignEvent(ctx context.Context, userID string, event *nostr.Event) (string, error) {
	h.logger.Debug("Signing event with NIP-46", zap.String("user_id", userID))

	// Connect to nsecbunker relay and send signing request
	// Actual implementation would:
	// 1. Connect to relayURL (nsecbunker)
	// 2. Create NIP-46 "sign_event" request
	// 3. Wait for signature response
	// 4. Return signed event

	return "", fmt.Errorf("signing not yet implemented")
}

// GetUserPublicKey retrieves a user's public key
func (h *NIP46Handler) GetUserPublicKey(ctx context.Context, userID string) (string, error) {
	h.logger.Debug("Getting user public key", zap.String("user_id", userID))

	// In Nostr, userID == pubkey, so just return it
	return userID, nil
}

// EncryptContent encrypts content for a user
// Uses NIP-44 encryption
func (h *NIP46Handler) EncryptContent(ctx context.Context, userID string, content string) (string, error) {
	h.logger.Debug("Encrypting content", zap.String("user_id", userID))

	// Stub: actual implementation would use NIP-44 encryption
	return "", fmt.Errorf("encryption not yet implemented")
}

// DecryptContent decrypts content for a user
// Uses NIP-44 decryption
func (h *NIP46Handler) DecryptContent(ctx context.Context, userID string, encrypted string) (string, error) {
	h.logger.Debug("Decrypting content", zap.String("user_id", userID))

	// Stub: actual implementation would use NIP-44 decryption
	return "", fmt.Errorf("decryption not yet implemented")
}

// Logout invalidates a session
func (h *NIP46Handler) Logout(ctx context.Context, sessionID string) error {
	h.logger.Debug("Logging out session", zap.String("session_id", sessionID))

	return h.sessionStore.DeleteSession(ctx, sessionID)
}
