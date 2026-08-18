package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/encryption"
	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/metrics"
	"github.com/google/uuid"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip04"
	"github.com/nbd-wtf/go-nostr/nip44"
	"go.uber.org/zap"
)

// signerSessionEntry caches a signer-session token → pubkey resolution.
type signerSessionEntry struct {
	pubkey  string
	expires time.Time
}

// NIP46Handler manages NIP-46 authentication with nsecbunker
type NIP46Handler struct {
	relayURL     string
	sessionStore SessionStore
	logger       *zap.Logger

	// Client keypair (ephemeral, generated per-instance)
	clientPrivateKey string
	clientPublicKey  string

	// Active connections to bunkers
	mu          sync.RWMutex
	connections map[string]*BunkerConnection

	// Unified-auth: validate tokens against the Cloistr signer service.
	// Empty signerURL disables the fallback branch entirely.
	signerURL    string
	httpClient   *http.Client
	sessionMu    sync.RWMutex
	sessionCache map[string]signerSessionEntry

	// nostrConnectRelay is used for the signer-as-bunker bootstrap (Option D).
	// InitiateNostrConnect posts a nostrconnect:// URI to this relay and waits
	// for the signer to publish a kind-24133 ACK.
	nostrConnectRelay string
}

// SessionStore interface for managing sessions
type SessionStore interface {
	SaveSession(ctx context.Context, session *Session) error
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	GetSessionByToken(ctx context.Context, token string) (*Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	SetNIP46Challenge(ctx context.Context, challengeID string, data *ChallengeData, ttl time.Duration) error
	GetNIP46Challenge(ctx context.Context, challengeID string) (*ChallengeData, error)
	DeleteNIP46Challenge(ctx context.Context, challengeID string) error
	// ConsumeNIP46Challenge atomically fetches AND deletes a challenge (Redis
	// GETDEL): the data is returned to exactly one caller, concurrent callers
	// get nil. Use as the one-time-use gate so a captured/replayed signed
	// challenge cannot mint more than one session (TOCTOU-free).
	ConsumeNIP46Challenge(ctx context.Context, challengeID string) (*ChallengeData, error)
}

// Session represents an authenticated session
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"` // Nostr pubkey (hex)
	Email     string    `json:"email"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// ChallengeData stores challenge information for verification
type ChallengeData struct {
	Challenge        string `json:"challenge"`
	BunkerPubkey     string `json:"bunker_pubkey"`
	RelayURL         string `json:"relay_url"`
	ClientPrivateKey string `json:"client_private_key"`
	CreatedAt        int64  `json:"created_at"`
}

// BunkerConnection represents an active connection to a bunker
type BunkerConnection struct {
	BunkerPubkey     string
	UserPubkey       string
	RelayURL         string
	Secret           string
	ClientPrivateKey string
	Connected        bool
	LastActivity     time.Time
}

// BunkerURL represents a parsed bunker:// URL
type BunkerURL struct {
	BunkerPubkey string
	RelayURL     string
	Secret       string
}

// NIP46Request represents a NIP-46 JSON-RPC request
type NIP46Request struct {
	ID     string   `json:"id"`
	Method string   `json:"method"`
	Params []string `json:"params"`
}

// NIP46Response represents a NIP-46 JSON-RPC response
type NIP46Response struct {
	ID     string `json:"id"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// NewNIP46Handler creates a new NIP-46 auth handler
func NewNIP46Handler(
	relayURL string,
	sessionStore SessionStore,
	logger *zap.Logger,
) (*NIP46Handler, error) {
	logger.Info("Initializing NIP-46 auth handler", zap.String("relay_url", relayURL))

	// Generate ephemeral client keypair
	clientPrivateKey := generatePrivateKey()
	clientPubkey, err := nostr.GetPublicKey(clientPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to derive public key: %w", err)
	}

	return &NIP46Handler{
		relayURL:         relayURL,
		sessionStore:     sessionStore,
		logger:           logger,
		clientPrivateKey: clientPrivateKey,
		clientPublicKey:  clientPubkey,
		connections:      make(map[string]*BunkerConnection),
		httpClient:       &http.Client{Timeout: 5 * time.Second},
		sessionCache:     make(map[string]signerSessionEntry),
	}, nil
}

// WithSignerURL configures the handler to fall back to the Cloistr signer
// service when a Redis session lookup misses.  Must be called before the
// handler is used; it is safe to call from main after construction.
// Setting an empty string disables the fallback (default behaviour).
func (h *NIP46Handler) WithSignerURL(signerURL string) {
	h.signerURL = signerURL
}

// WithNostrConnectRelay sets the relay used for the Option D signer-as-bunker
// bootstrap.  Must be called before InitiateNostrConnect is used; safe to call
// from main after construction.
func (h *NIP46Handler) WithNostrConnectRelay(relayURL string) {
	h.nostrConnectRelay = relayURL
}

// ParseBunkerURL parses a bunker:// URL
// Format: bunker://<bunker-pubkey>?relay=<wss://relay>&secret=<optional-secret>
func ParseBunkerURL(bunkerURL string) (*BunkerURL, error) {
	if !strings.HasPrefix(bunkerURL, "bunker://") {
		return nil, fmt.Errorf("invalid bunker URL: must start with bunker://")
	}

	// Remove the bunker:// prefix
	rest := strings.TrimPrefix(bunkerURL, "bunker://")

	// Split pubkey from query string
	parts := strings.SplitN(rest, "?", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid bunker URL: missing query parameters")
	}

	bunkerPubkey := parts[0]
	if len(bunkerPubkey) != 64 {
		return nil, fmt.Errorf("invalid bunker pubkey: must be 64 hex characters")
	}

	// Parse query parameters
	query, err := url.ParseQuery(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid query parameters: %w", err)
	}

	relayURL := query.Get("relay")
	if relayURL == "" {
		return nil, fmt.Errorf("invalid bunker URL: missing relay parameter")
	}

	return &BunkerURL{
		BunkerPubkey: bunkerPubkey,
		RelayURL:     relayURL,
		Secret:       query.Get("secret"),
	}, nil
}

// AuthChallenge represents a NIP-46 authentication challenge
type AuthChallenge struct {
	ID           string `json:"id"`
	Challenge    string `json:"challenge"`
	BunkerPubkey string `json:"bunker_pubkey,omitempty"`
	RelayURL     string `json:"relay_url,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	ExpiresAt    int64  `json:"expires_at"`
}

// CreateAuthChallenge creates a new NIP-46 authentication challenge
func (h *NIP46Handler) CreateAuthChallenge(ctx context.Context, bunkerURL string) (*AuthChallenge, error) {
	h.logger.Debug("Creating auth challenge", zap.String("bunker_url", bunkerURL))

	// Parse the bunker URL
	parsed, err := ParseBunkerURL(bunkerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid bunker URL: %w", err)
	}

	// Generate challenge ID and content
	id := uuid.New().String()
	challenge := generateRandomHex(32)

	now := time.Now()
	expiresAt := now.Add(5 * time.Minute)

	// Generate a fresh client keypair for this challenge
	clientPrivKey := generatePrivateKey()

	// Store challenge data in Redis with TTL
	challengeData := &ChallengeData{
		Challenge:        challenge,
		BunkerPubkey:     parsed.BunkerPubkey,
		RelayURL:         parsed.RelayURL,
		ClientPrivateKey: clientPrivKey,
		CreatedAt:        now.Unix(),
	}

	if err := h.sessionStore.SetNIP46Challenge(ctx, id, challengeData, 5*time.Minute); err != nil {
		h.logger.Error("Failed to store challenge", zap.Error(err))
		return nil, err
	}

	return &AuthChallenge{
		ID:           id,
		Challenge:    challenge,
		BunkerPubkey: parsed.BunkerPubkey,
		RelayURL:     parsed.RelayURL,
		CreatedAt:    now.Unix(),
		ExpiresAt:    expiresAt.Unix(),
	}, nil
}

// ConnectToBunker initiates a NIP-46 connection to a bunker
func (h *NIP46Handler) ConnectToBunker(ctx context.Context, challengeID string) (*Session, error) {
	h.logger.Debug("Connecting to bunker", zap.String("challenge_id", challengeID))

	// Retrieve challenge from store
	challengeData, err := h.sessionStore.GetNIP46Challenge(ctx, challengeID)
	if err != nil {
		return nil, fmt.Errorf("challenge retrieval failed: %w", err)
	}
	if challengeData == nil {
		return nil, fmt.Errorf("challenge not found or expired")
	}

	// Connect to the relay
	relay, err := nostr.RelayConnect(ctx, challengeData.RelayURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to relay: %w", err)
	}
	defer func() { _ = relay.Close() }()

	// Get client public key from the stored private key
	clientPubkey, err := nostr.GetPublicKey(challengeData.ClientPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to derive client pubkey: %w", err)
	}

	// Create connect request
	requestID := generateRandomHex(16)
	connectRequest := NIP46Request{
		ID:     requestID,
		Method: "connect",
		Params: []string{clientPubkey, challengeData.Challenge},
	}

	requestJSON, err := json.Marshal(connectRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Encrypt the request using NIP-44
	conversationKey, err := nip44.GenerateConversationKey(challengeData.ClientPrivateKey, challengeData.BunkerPubkey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate conversation key: %w", err)
	}

	encryptedContent, err := nip44.Encrypt(string(requestJSON), conversationKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt request: %w", err)
	}

	// Create the NIP-46 event (kind 24133)
	event := nostr.Event{
		Kind:      24133,
		PubKey:    clientPubkey,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags: nostr.Tags{
			{"p", challengeData.BunkerPubkey},
		},
		Content: encryptedContent,
	}

	// Sign the event
	if err := event.Sign(challengeData.ClientPrivateKey); err != nil {
		return nil, fmt.Errorf("failed to sign event: %w", err)
	}

	// Subscribe to responses before publishing
	responseCh := make(chan *nostr.Event, 1)
	sub, err := relay.Subscribe(ctx, nostr.Filters{
		{
			Kinds:   []int{24133},
			Authors: []string{challengeData.BunkerPubkey},
			Tags:    nostr.TagMap{"p": []string{clientPubkey}},
			Since:   &event.CreatedAt,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe: %w", err)
	}
	defer sub.Unsub()

	// Publish the connect request
	if err := relay.Publish(ctx, event); err != nil {
		return nil, fmt.Errorf("failed to publish event: %w", err)
	}

	h.logger.Debug("Published connect request, waiting for response",
		zap.String("event_id", event.ID),
		zap.String("bunker_pubkey", challengeData.BunkerPubkey))

	// Wait for response with timeout
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	go func() {
		for ev := range sub.Events {
			responseCh <- ev
			return
		}
	}()

	select {
	case responseEvent := <-responseCh:
		return h.handleConnectResponse(ctx, responseEvent, challengeData, clientPubkey, requestID)
	case <-timeout.C:
		return nil, fmt.Errorf("timeout waiting for bunker response")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// handleConnectResponse processes the bunker's response to a connect request
func (h *NIP46Handler) handleConnectResponse(
	ctx context.Context,
	event *nostr.Event,
	challengeData *ChallengeData,
	clientPubkey string,
	requestID string,
) (*Session, error) {
	// Verify the event signature
	ok, err := event.CheckSignature()
	if err != nil || !ok {
		return nil, fmt.Errorf("invalid event signature")
	}

	// Decrypt the response
	conversationKey, err := nip44.GenerateConversationKey(challengeData.ClientPrivateKey, challengeData.BunkerPubkey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate conversation key: %w", err)
	}

	decrypted, err := nip44.Decrypt(event.Content, conversationKey)
	if err != nil {
		// Try NIP-04 as fallback (some bunkers still use it)
		sharedSecret, secretErr := nip04.ComputeSharedSecret(challengeData.ClientPrivateKey, challengeData.BunkerPubkey)
		if secretErr != nil {
			return nil, fmt.Errorf("failed to compute shared secret: %w", secretErr)
		}
		decrypted, err = nip04.Decrypt(event.Content, sharedSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt response: %w", err)
		}
	}

	// Parse the response
	var response NIP46Response
	if err := json.Unmarshal([]byte(decrypted), &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for errors
	if response.Error != "" {
		return nil, fmt.Errorf("bunker error: %s", response.Error)
	}

	// Verify request ID matches
	if response.ID != requestID {
		return nil, fmt.Errorf("response ID mismatch")
	}

	// Get the user's public key
	userPubkey, err := h.getUserPubkeyFromBunker(ctx, challengeData)
	if err != nil {
		return nil, fmt.Errorf("failed to get user pubkey: %w", err)
	}

	h.logger.Info("Successfully connected to bunker", zap.String("user_pubkey", userPubkey))

	// Store the connection
	h.mu.Lock()
	h.connections[userPubkey] = &BunkerConnection{
		BunkerPubkey:     challengeData.BunkerPubkey,
		UserPubkey:       userPubkey,
		RelayURL:         challengeData.RelayURL,
		ClientPrivateKey: challengeData.ClientPrivateKey,
		Connected:        true,
		LastActivity:     time.Now(),
	}
	h.mu.Unlock()

	// Create authenticated session
	session := &Session{
		ID:        uuid.New().String(),
		UserID:    userPubkey,
		Token:     generateSecureToken(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	// Save session
	if err := h.sessionStore.SaveSession(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to save session: %w", err)
	}

	// Clean up the challenge
	_ = h.sessionStore.DeleteNIP46Challenge(ctx, requestID)

	return session, nil
}

// getUserPubkeyFromBunker requests the user's public key from the bunker
func (h *NIP46Handler) getUserPubkeyFromBunker(ctx context.Context, challengeData *ChallengeData) (string, error) {
	relay, err := nostr.RelayConnect(ctx, challengeData.RelayURL)
	if err != nil {
		return "", fmt.Errorf("failed to connect to relay: %w", err)
	}
	defer func() { _ = relay.Close() }()

	clientPubkey, _ := nostr.GetPublicKey(challengeData.ClientPrivateKey)

	// Create get_public_key request
	requestID := generateRandomHex(16)
	request := NIP46Request{
		ID:     requestID,
		Method: "get_public_key",
		Params: []string{},
	}

	requestJSON, _ := json.Marshal(request)

	// Encrypt and send
	conversationKey, _ := nip44.GenerateConversationKey(challengeData.ClientPrivateKey, challengeData.BunkerPubkey)
	encryptedContent, _ := nip44.Encrypt(string(requestJSON), conversationKey)

	event := nostr.Event{
		Kind:      24133,
		PubKey:    clientPubkey,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      nostr.Tags{{"p", challengeData.BunkerPubkey}},
		Content:   encryptedContent,
	}
	if err := event.Sign(challengeData.ClientPrivateKey); err != nil {
		return "", fmt.Errorf("failed to sign event: %w", err)
	}

	// Subscribe for response
	sub, _ := relay.Subscribe(ctx, nostr.Filters{{
		Kinds:   []int{24133},
		Authors: []string{challengeData.BunkerPubkey},
		Tags:    nostr.TagMap{"p": []string{clientPubkey}},
		Since:   &event.CreatedAt,
	}})
	defer sub.Unsub()

	if err := relay.Publish(ctx, event); err != nil {
		// Fail fast. Without this we fall through to the wait below and report a
		// generic timeout ~30s later, hiding a cause that was knowable now.
		return "", fmt.Errorf("failed to publish request to relay: %w", err)
	}

	// Wait for response
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case responseEvent := <-sub.Events:
			decrypted, err := nip44.Decrypt(responseEvent.Content, conversationKey)
			if err != nil {
				continue
			}

			var response NIP46Response
			if err := json.Unmarshal([]byte(decrypted), &response); err != nil {
				continue
			}

			if response.ID == requestID && response.Result != "" {
				return response.Result, nil
			}
		case <-timeout.C:
			return "", fmt.Errorf("timeout waiting for public key")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// VerifyAuthSignature verifies a pre-signed authentication event
// This is an alternative flow where the client provides a signed event directly
func (h *NIP46Handler) VerifyAuthSignature(ctx context.Context, challengeID string, signedEventJSON string) (*Session, error) {
	h.logger.Debug("Verifying auth signature", zap.String("challenge_id", challengeID))

	// Retrieve challenge from store
	challengeData, err := h.sessionStore.GetNIP46Challenge(ctx, challengeID)
	if err != nil {
		metrics.AuthAttemptsTotal.WithLabelValues("nip07", "failure").Inc()
		return nil, fmt.Errorf("challenge retrieval failed: %w", err)
	}
	if challengeData == nil {
		metrics.AuthAttemptsTotal.WithLabelValues("nip07", "failure").Inc()
		return nil, fmt.Errorf("challenge not found or expired")
	}

	// Parse the signed event
	var event nostr.Event
	if err := json.Unmarshal([]byte(signedEventJSON), &event); err != nil {
		metrics.AuthAttemptsTotal.WithLabelValues("nip07", "failure").Inc()
		return nil, fmt.Errorf("invalid event JSON: %w", err)
	}

	// Verify signature
	ok, err := event.CheckSignature()
	if err != nil || !ok {
		metrics.AuthAttemptsTotal.WithLabelValues("nip07", "failure").Inc()
		return nil, fmt.Errorf("invalid signature")
	}

	// Verify the challenge is in the event content
	if event.Content != challengeData.Challenge {
		metrics.AuthAttemptsTotal.WithLabelValues("nip07", "failure").Inc()
		return nil, fmt.Errorf("challenge mismatch")
	}

	// Verify event is recent (within 5 minutes)
	eventTime := time.Unix(int64(event.CreatedAt), 0)
	if time.Since(eventTime) > 5*time.Minute {
		metrics.AuthAttemptsTotal.WithLabelValues("nip07", "failure").Inc()
		return nil, fmt.Errorf("event too old")
	}

	h.logger.Info("Signature verified", zap.String("pubkey", event.PubKey))

	// Create session
	session := &Session{
		ID:        uuid.New().String(),
		UserID:    event.PubKey,
		Token:     generateSecureToken(),
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	if err := h.sessionStore.SaveSession(ctx, session); err != nil {
		metrics.AuthAttemptsTotal.WithLabelValues("nip07", "failure").Inc()
		return nil, fmt.Errorf("failed to save session: %w", err)
	}

	// Record successful auth and increment active sessions
	metrics.AuthAttemptsTotal.WithLabelValues("nip07", "success").Inc()
	metrics.ActiveSessions.Inc()

	// Clean up challenge
	_ = h.sessionStore.DeleteNIP46Challenge(ctx, challengeID)

	return session, nil
}

// ValidateSession validates an existing session token.
//
// Primary path: Redis lookup (NIP-46 bunker and NIP-07 client-side sessions).
// Fallback path: when the Redis lookup misses AND signerURL is configured,
// the token is validated against the Cloistr signer /api/v1/users/me endpoint.
// A transient in-memory-cached Session is returned; it is never persisted to
// Redis so the existing write paths are untouched.
func (h *NIP46Handler) ValidateSession(ctx context.Context, token string) (*Session, error) {
	h.logger.Debug("Validating session token")

	session, err := h.sessionStore.GetSessionByToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("session lookup failed: %w", err)
	}
	if session == nil {
		// Redis miss — try the signer fallback if configured.
		if s := h.validateSignerSession(ctx, token); s != nil {
			return s, nil
		}
		return nil, fmt.Errorf("invalid session token")
	}

	// Check expiration
	if time.Now().After(session.ExpiresAt) {
		// Clean up expired session
		_ = h.sessionStore.DeleteSession(ctx, session.ID)
		return nil, fmt.Errorf("session expired")
	}

	return session, nil
}

// validateSignerSession forwards the token to the Cloistr signer service and,
// on success, returns a transient Session (not stored in Redis).  Results are
// cached in-memory for 2 minutes to avoid hammering the signer on every request.
// Returns nil when signerURL is empty or the signer rejects the token.
func (h *NIP46Handler) validateSignerSession(ctx context.Context, token string) *Session {
	if h.signerURL == "" || token == "" {
		return nil
	}

	cacheKey := token

	// Fast path: in-memory cache hit.
	h.sessionMu.RLock()
	if e, ok := h.sessionCache[cacheKey]; ok && time.Now().Before(e.expires) {
		h.sessionMu.RUnlock()
		now := time.Now()
		return &Session{
			UserID:    e.pubkey,
			Token:     token,
			ExpiresAt: e.expires,
			CreatedAt: now,
		}
	}
	h.sessionMu.RUnlock()

	// Call GET <signerURL>/api/v1/users/me with the token forwarded as both
	// an auth_token cookie and an Authorization: Bearer header.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.signerURL+"/api/v1/users/me", nil)
	if err != nil {
		h.logger.Warn("signer session: failed to build request", zap.Error(err))
		return nil
	}
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: token})
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.logger.Warn("signer session: request failed", zap.Error(err))
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var body struct {
		Pubkey string `json:"pubkey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Pubkey == "" {
		h.logger.Warn("signer session: invalid response body", zap.Error(err))
		return nil
	}

	now := time.Now()
	expiry := now.Add(15 * time.Minute)

	// Populate in-memory cache.
	h.sessionMu.Lock()
	h.sessionCache[cacheKey] = signerSessionEntry{pubkey: body.Pubkey, expires: now.Add(2 * time.Minute)}
	h.sessionMu.Unlock()

	h.logger.Debug("signer session validated", zap.String("pubkey", body.Pubkey[:min(16, len(body.Pubkey))]+"..."))

	return &Session{
		UserID:    body.Pubkey,
		Token:     token,
		ExpiresAt: expiry,
		CreatedAt: now,
	}
}

// min returns the smaller of a and b (Go 1.21+ has this built-in; kept local
// for compatibility with Go 1.20 and earlier toolchains that may be in CI).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SignEvent signs a Nostr event using NIP-46 remote signing
func (h *NIP46Handler) SignEvent(ctx context.Context, userPubkey string, event *nostr.Event) error {
	h.logger.Debug("Signing event with NIP-46", zap.String("user_pubkey", userPubkey))

	// Get the connection for this user
	h.mu.RLock()
	conn, ok := h.connections[userPubkey]
	h.mu.RUnlock()

	if !ok || !conn.Connected {
		return encryption.ErrNoSignerConnection
	}

	// Connect to relay
	relay, err := nostr.RelayConnect(ctx, conn.RelayURL)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %w", err)
	}
	defer func() { _ = relay.Close() }()

	clientPubkey, _ := nostr.GetPublicKey(conn.ClientPrivateKey)

	// Prepare unsigned event for signing
	unsignedEvent := map[string]interface{}{
		"kind":       event.Kind,
		"content":    event.Content,
		"tags":       event.Tags,
		"created_at": event.CreatedAt,
	}
	unsignedJSON, _ := json.Marshal(unsignedEvent)

	// Create sign_event request
	requestID := generateRandomHex(16)
	request := NIP46Request{
		ID:     requestID,
		Method: "sign_event",
		Params: []string{string(unsignedJSON)},
	}

	requestJSON, _ := json.Marshal(request)

	// Encrypt
	conversationKey, _ := nip44.GenerateConversationKey(conn.ClientPrivateKey, conn.BunkerPubkey)
	encryptedContent, _ := nip44.Encrypt(string(requestJSON), conversationKey)

	// Create and sign the request event
	reqEvent := nostr.Event{
		Kind:      24133,
		PubKey:    clientPubkey,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      nostr.Tags{{"p", conn.BunkerPubkey}},
		Content:   encryptedContent,
	}
	if err := reqEvent.Sign(conn.ClientPrivateKey); err != nil {
		// Matches the checked idiom already used at lines 321 and 490.
		// An unsigned event is rejected by the relay, so swallowing this
		// turned a knowable signing fault into a 30s wait for a reply
		// that could never arrive.
		return fmt.Errorf("failed to sign request event: %w", err)
	}

	// Subscribe and publish
	sub, _ := relay.Subscribe(ctx, nostr.Filters{{
		Kinds:   []int{24133},
		Authors: []string{conn.BunkerPubkey},
		Tags:    nostr.TagMap{"p": []string{clientPubkey}},
		Since:   &reqEvent.CreatedAt,
	}})
	defer sub.Unsub()

	if err := relay.Publish(ctx, reqEvent); err != nil {
		// Fail fast. Without this we fall through to the wait below and report a
		// generic timeout ~30s later, hiding a cause that was knowable now.
		return fmt.Errorf("failed to publish request to relay: %w", err)
	}

	// Wait for response
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case responseEvent := <-sub.Events:
			decrypted, err := nip44.Decrypt(responseEvent.Content, conversationKey)
			if err != nil {
				continue
			}

			var response NIP46Response
			if err := json.Unmarshal([]byte(decrypted), &response); err != nil {
				continue
			}

			if response.ID != requestID {
				continue
			}

			if response.Error != "" {
				return fmt.Errorf("bunker error: %s", response.Error)
			}

			// Parse the signed event
			var signedEvent nostr.Event
			if err := json.Unmarshal([]byte(response.Result), &signedEvent); err != nil {
				return fmt.Errorf("failed to parse signed event: %w", err)
			}

			// Copy signed fields back
			event.ID = signedEvent.ID
			event.PubKey = signedEvent.PubKey
			event.Sig = signedEvent.Sig

			return nil
		case <-timeout.C:
			return fmt.Errorf("timeout waiting for signature")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// EncryptContent encrypts content using NIP-44 via the bunker
func (h *NIP46Handler) EncryptContent(ctx context.Context, userPubkey string, recipientPubkey string, plaintext string) (string, error) {
	h.logger.Debug("Encrypting content via NIP-46",
		zap.String("user_pubkey", userPubkey),
		zap.String("recipient_pubkey", recipientPubkey))

	h.mu.RLock()
	conn, ok := h.connections[userPubkey]
	h.mu.RUnlock()

	if !ok || !conn.Connected {
		return "", encryption.ErrNoSignerConnection
	}

	relay, err := nostr.RelayConnect(ctx, conn.RelayURL)
	if err != nil {
		return "", fmt.Errorf("failed to connect to relay: %w", err)
	}
	defer func() { _ = relay.Close() }()

	clientPubkey, _ := nostr.GetPublicKey(conn.ClientPrivateKey)

	// Create nip44_encrypt request
	requestID := generateRandomHex(16)
	request := NIP46Request{
		ID:     requestID,
		Method: "nip44_encrypt",
		Params: []string{recipientPubkey, plaintext},
	}

	requestJSON, _ := json.Marshal(request)
	conversationKey, _ := nip44.GenerateConversationKey(conn.ClientPrivateKey, conn.BunkerPubkey)
	encryptedContent, _ := nip44.Encrypt(string(requestJSON), conversationKey)

	reqEvent := nostr.Event{
		Kind:      24133,
		PubKey:    clientPubkey,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      nostr.Tags{{"p", conn.BunkerPubkey}},
		Content:   encryptedContent,
	}
	if err := reqEvent.Sign(conn.ClientPrivateKey); err != nil {
		// Matches the checked idiom already used at lines 321 and 490.
		// An unsigned event is rejected by the relay, so swallowing this
		// turned a knowable signing fault into a 30s wait for a reply
		// that could never arrive.
		return "", fmt.Errorf("failed to sign request event: %w", err)
	}

	sub, _ := relay.Subscribe(ctx, nostr.Filters{{
		Kinds:   []int{24133},
		Authors: []string{conn.BunkerPubkey},
		Tags:    nostr.TagMap{"p": []string{clientPubkey}},
		Since:   &reqEvent.CreatedAt,
	}})
	defer sub.Unsub()

	if err := relay.Publish(ctx, reqEvent); err != nil {
		// Fail fast. Without this we fall through to the wait below and report a
		// generic timeout ~30s later, hiding a cause that was knowable now.
		return "", fmt.Errorf("failed to publish request to relay: %w", err)
	}

	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case responseEvent := <-sub.Events:
			decrypted, err := nip44.Decrypt(responseEvent.Content, conversationKey)
			if err != nil {
				continue
			}

			var response NIP46Response
			if err := json.Unmarshal([]byte(decrypted), &response); err != nil {
				continue
			}

			if response.ID == requestID {
				if response.Error != "" {
					return "", fmt.Errorf("bunker error: %s", response.Error)
				}
				return response.Result, nil
			}
		case <-timeout.C:
			return "", fmt.Errorf("timeout waiting for encryption")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// DecryptContent decrypts content using NIP-44 via the bunker
func (h *NIP46Handler) DecryptContent(ctx context.Context, userPubkey string, senderPubkey string, ciphertext string) (string, error) {
	h.logger.Debug("Decrypting content via NIP-46",
		zap.String("user_pubkey", userPubkey),
		zap.String("sender_pubkey", senderPubkey))

	h.mu.RLock()
	conn, ok := h.connections[userPubkey]
	h.mu.RUnlock()

	if !ok || !conn.Connected {
		return "", encryption.ErrNoSignerConnection
	}

	relay, err := nostr.RelayConnect(ctx, conn.RelayURL)
	if err != nil {
		return "", fmt.Errorf("failed to connect to relay: %w", err)
	}
	defer func() { _ = relay.Close() }()

	clientPubkey, _ := nostr.GetPublicKey(conn.ClientPrivateKey)

	// Create nip44_decrypt request
	requestID := generateRandomHex(16)
	request := NIP46Request{
		ID:     requestID,
		Method: "nip44_decrypt",
		Params: []string{senderPubkey, ciphertext},
	}

	requestJSON, _ := json.Marshal(request)
	conversationKey, _ := nip44.GenerateConversationKey(conn.ClientPrivateKey, conn.BunkerPubkey)
	encryptedContent, _ := nip44.Encrypt(string(requestJSON), conversationKey)

	reqEvent := nostr.Event{
		Kind:      24133,
		PubKey:    clientPubkey,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      nostr.Tags{{"p", conn.BunkerPubkey}},
		Content:   encryptedContent,
	}
	if err := reqEvent.Sign(conn.ClientPrivateKey); err != nil {
		// Matches the checked idiom already used at lines 321 and 490.
		// An unsigned event is rejected by the relay, so swallowing this
		// turned a knowable signing fault into a 30s wait for a reply
		// that could never arrive.
		return "", fmt.Errorf("failed to sign request event: %w", err)
	}

	sub, _ := relay.Subscribe(ctx, nostr.Filters{{
		Kinds:   []int{24133},
		Authors: []string{conn.BunkerPubkey},
		Tags:    nostr.TagMap{"p": []string{clientPubkey}},
		Since:   &reqEvent.CreatedAt,
	}})
	defer sub.Unsub()

	if err := relay.Publish(ctx, reqEvent); err != nil {
		return "", fmt.Errorf("failed to publish request to relay: %w", err)
	}

	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case responseEvent := <-sub.Events:
			decrypted, err := nip44.Decrypt(responseEvent.Content, conversationKey)
			if err != nil {
				continue
			}

			var response NIP46Response
			if err := json.Unmarshal([]byte(decrypted), &response); err != nil {
				continue
			}

			if response.ID == requestID {
				if response.Error != "" {
					return "", fmt.Errorf("bunker error: %s", response.Error)
				}
				return response.Result, nil
			}
		case <-timeout.C:
			return "", fmt.Errorf("timeout waiting for decryption")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// GetUserPublicKey returns the user's public key (same as userID in Nostr)
func (h *NIP46Handler) GetUserPublicKey(ctx context.Context, userID string) (string, error) {
	return userID, nil
}

// Logout invalidates a session and disconnects from bunker
func (h *NIP46Handler) Logout(ctx context.Context, sessionID string) error {
	h.logger.Debug("Logging out session", zap.String("session_id", sessionID))

	session, err := h.sessionStore.GetSession(ctx, sessionID)
	if err == nil && session != nil {
		// Remove bunker connection
		h.mu.Lock()
		delete(h.connections, session.UserID)
		h.mu.Unlock()
	}

	return h.sessionStore.DeleteSession(ctx, sessionID)
}

// HasBunkerConnection reports whether the user has a live, connected bunker
// session.  Thread-safe; safe to call from any goroutine.
func (h *NIP46Handler) HasBunkerConnection(userPubkey string) bool {
	h.mu.RLock()
	conn := h.connections[userPubkey]
	h.mu.RUnlock()
	return conn != nil && conn.Connected
}

// InitiateNostrConnect creates an ephemeral keypair, builds a nostrconnect://
// URI, persists the bootstrap state in Redis (TTL 90s), and starts a
// background goroutine that listens on the relay for the signer's ACK.
//
// Crypto flow:
//   - clientPrivKey is a fresh random secp256k1 scalar (nostr.GeneratePrivateKey)
//   - The signer ACK is a kind-24133 event signed by userPubkey and p-tagged to
//     clientPubkey.  Its content is NIP-44 encrypted using the conversation key
//     derived as: nip44.GenerateConversationKey(userPubkey, clientPrivKey)
//     i.e. ECDH(clientPrivKey * G_userPubkey) — our private key against the
//     user's public key.
//   - NIP-04 is tried as a fallback if NIP-44 decryption fails (same pattern as
//     handleConnectResponse).
//   - The JSON-RPC result field is accepted when it equals the secret or is
//     "ack"/"pong" (mirrors the acceptance logic in handleConnectResponse).
//
// Returns (uri, nonce, nil) immediately; the goroutine completes asynchronously.
func (h *NIP46Handler) InitiateNostrConnect(ctx context.Context, userPubkey, relayURL, appName string) (uri string, nonce string, err error) {
	h.logger.Debug("InitiateNostrConnect",
		zap.String("user_pubkey", userPubkey[:min(16, len(userPubkey))]+"..."),
		zap.String("relay_url", relayURL))

	// Generate ephemeral keypair for this bootstrap attempt.
	clientPrivKey := generatePrivateKey()
	clientPubkey, err := nostr.GetPublicKey(clientPrivKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to derive client public key: %w", err)
	}

	// Generate random secret (carried in the URI, echoed back in the ACK).
	secret := generateRandomHex(16)

	// Generate single-use nonce that keys the Redis bootstrap state.
	nonce = generateRandomHex(16)

	// Build the nostrconnect:// URI.
	uri = "nostrconnect://" + clientPubkey +
		"?relay=" + url.QueryEscape(relayURL) +
		"&secret=" + secret +
		"&name=" + url.QueryEscape(appName)

	// Persist bootstrap state under key "nc:<nonce>" with a 90s TTL.
	// We reuse ChallengeData: BunkerPubkey carries userPubkey (the signer
	// signs as the user's key), Challenge carries the secret.
	bootstrapData := &ChallengeData{
		Challenge:        secret,
		BunkerPubkey:     userPubkey,
		RelayURL:         relayURL,
		ClientPrivateKey: clientPrivKey,
		CreatedAt:        time.Now().Unix(),
	}
	if err := h.sessionStore.SetNIP46Challenge(ctx, "nc:"+nonce, bootstrapData, 90*time.Second); err != nil {
		return "", "", fmt.Errorf("failed to persist nostrconnect bootstrap state: %w", err)
	}

	// Launch background goroutine — NOT tied to the request context.
	bgCtx, bgCancel := context.WithTimeout(context.Background(), 90*time.Second)
	go func() {
		defer bgCancel()
		h.awaitNostrConnectACK(bgCtx, nonce, userPubkey, relayURL, clientPrivKey, clientPubkey, secret)
	}()

	return uri, nonce, nil
}

// awaitNostrConnectACK is the background goroutine body for InitiateNostrConnect.
// It connects to the relay, subscribes for the signer's ACK event, validates it,
// and on success writes the connection into h.connections.
func (h *NIP46Handler) awaitNostrConnectACK(
	ctx context.Context,
	nonce, userPubkey, relayURL, clientPrivKey, clientPubkey, secret string,
) {
	logger := h.logger.With(
		zap.String("user_pubkey", userPubkey[:min(16, len(userPubkey))]+"..."),
		zap.String("nonce", nonce),
	)

	relay, err := nostr.RelayConnect(ctx, relayURL)
	if err != nil {
		logger.Warn("nostrconnect: failed to connect to relay", zap.Error(err))
		h.cleanupBootstrapState(nonce)
		return
	}
	defer func() { _ = relay.Close() }()

	now := nostr.Timestamp(time.Now().Unix())
	sub, err := relay.Subscribe(ctx, nostr.Filters{
		{
			Kinds:   []int{24133},
			Authors: []string{userPubkey},
			Tags:    nostr.TagMap{"p": []string{clientPubkey}},
			Since:   &now,
		},
	})
	if err != nil {
		logger.Warn("nostrconnect: failed to subscribe", zap.Error(err))
		h.cleanupBootstrapState(nonce)
		return
	}
	defer sub.Unsub()

	// NIP-44 conversation key: our clientPrivKey against the user's public key.
	// nip44.GenerateConversationKey(pub, sk) — pubkey first, privkey second.
	conversationKey, err := nip44.GenerateConversationKey(userPubkey, clientPrivKey)
	if err != nil {
		logger.Warn("nostrconnect: failed to generate conversation key", zap.Error(err))
		h.cleanupBootstrapState(nonce)
		return
	}

	for {
		select {
		case ev, ok := <-sub.Events:
			if !ok {
				logger.Warn("nostrconnect: subscription closed before ACK")
				h.cleanupBootstrapState(nonce)
				return
			}

			// Verify the event signature.
			sigOK, sigErr := ev.CheckSignature()
			if sigErr != nil || !sigOK {
				logger.Warn("nostrconnect: event has invalid signature, skipping")
				continue
			}

			// Decrypt with NIP-44, fall back to NIP-04 (mirrors handleConnectResponse).
			decrypted, decErr := nip44.Decrypt(ev.Content, conversationKey)
			if decErr != nil {
				sharedSecret, ssErr := nip04.ComputeSharedSecret(clientPrivKey, userPubkey)
				if ssErr != nil {
					logger.Warn("nostrconnect: failed to compute NIP-04 shared secret, skipping event")
					continue
				}
				decrypted, decErr = nip04.Decrypt(ev.Content, sharedSecret)
				if decErr != nil {
					logger.Warn("nostrconnect: failed to decrypt event content, skipping")
					continue
				}
			}

			var response NIP46Response
			if jsonErr := json.Unmarshal([]byte(decrypted), &response); jsonErr != nil {
				logger.Warn("nostrconnect: failed to parse JSON-RPC response, skipping")
				continue
			}

			// Accept as ACK when: result equals our secret, or "ack", or "pong".
			// (Mirrors handleConnectResponse acceptance logic.)
			result := response.Result
			if result != secret && result != "ack" && result != "pong" {
				if response.Error != "" {
					logger.Warn("nostrconnect: signer returned error", zap.String("error", response.Error))
				} else {
					logger.Warn("nostrconnect: unexpected result, not an ACK", zap.String("result", result))
				}
				continue
			}

			// Success — register the connection.
			h.mu.Lock()
			h.connections[userPubkey] = &BunkerConnection{
				// BunkerPubkey == userPubkey: the signer signs as the user's key.
				BunkerPubkey:     userPubkey,
				UserPubkey:       userPubkey,
				RelayURL:         relayURL,
				ClientPrivateKey: clientPrivKey,
				Connected:        true,
				LastActivity:     time.Now(),
			}
			h.mu.Unlock()

			h.cleanupBootstrapState(nonce)
			logger.Info("nostrconnect: bunker connection established via signer ACK")
			return

		case <-ctx.Done():
			logger.Warn("nostrconnect: timed out waiting for signer ACK")
			h.cleanupBootstrapState(nonce)
			return
		}
	}
}

// cleanupBootstrapState removes the Redis nostrconnect bootstrap key.
// Errors are logged and swallowed — the key has a TTL so it will expire anyway.
func (h *NIP46Handler) cleanupBootstrapState(nonce string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.sessionStore.DeleteNIP46Challenge(cleanupCtx, "nc:"+nonce); err != nil {
		h.logger.Debug("nostrconnect: failed to clean up bootstrap state", zap.Error(err))
	}
}

// Helper functions

func generatePrivateKey() string {
	return nostr.GeneratePrivateKey()
}

func generateRandomHex(length int) string {
	bytes := make([]byte, length)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func generateSecureToken() string {
	return generateRandomHex(32)
}
