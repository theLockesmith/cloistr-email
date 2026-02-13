package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coldforge/coldforge-email/internal/metrics"
	"github.com/google/uuid"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip04"
	"github.com/nbd-wtf/go-nostr/nip44"
	"go.uber.org/zap"
)

// NIP46Handler manages NIP-46 authentication with nsecbunker
type NIP46Handler struct {
	relayURL       string
	sessionStore   SessionStore
	stalwartClient *StalwartClient
	logger         *zap.Logger

	// Client keypair (ephemeral, generated per-instance)
	clientPrivateKey string
	clientPublicKey  string

	// Active connections to bunkers
	mu          sync.RWMutex
	connections map[string]*BunkerConnection
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
	stalwartClient *StalwartClient,
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
		stalwartClient:   stalwartClient,
		logger:           logger,
		clientPrivateKey: clientPrivateKey,
		clientPublicKey:  clientPubkey,
		connections:      make(map[string]*BunkerConnection),
	}, nil
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
	defer relay.Close()

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
	defer relay.Close()

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
	event.Sign(challengeData.ClientPrivateKey)

	// Subscribe for response
	sub, _ := relay.Subscribe(ctx, nostr.Filters{{
		Kinds:   []int{24133},
		Authors: []string{challengeData.BunkerPubkey},
		Tags:    nostr.TagMap{"p": []string{clientPubkey}},
		Since:   &event.CreatedAt,
	}})
	defer sub.Unsub()

	relay.Publish(ctx, event)

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

// ValidateSession validates an existing session token
func (h *NIP46Handler) ValidateSession(ctx context.Context, token string) (*Session, error) {
	h.logger.Debug("Validating session token")

	session, err := h.sessionStore.GetSessionByToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("session lookup failed: %w", err)
	}
	if session == nil {
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

// SignEvent signs a Nostr event using NIP-46 remote signing
func (h *NIP46Handler) SignEvent(ctx context.Context, userPubkey string, event *nostr.Event) error {
	h.logger.Debug("Signing event with NIP-46", zap.String("user_pubkey", userPubkey))

	// Get the connection for this user
	h.mu.RLock()
	conn, ok := h.connections[userPubkey]
	h.mu.RUnlock()

	if !ok || !conn.Connected {
		return fmt.Errorf("no active bunker connection for user")
	}

	// Connect to relay
	relay, err := nostr.RelayConnect(ctx, conn.RelayURL)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %w", err)
	}
	defer relay.Close()

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
	reqEvent.Sign(conn.ClientPrivateKey)

	// Subscribe and publish
	sub, _ := relay.Subscribe(ctx, nostr.Filters{{
		Kinds:   []int{24133},
		Authors: []string{conn.BunkerPubkey},
		Tags:    nostr.TagMap{"p": []string{clientPubkey}},
		Since:   &reqEvent.CreatedAt,
	}})
	defer sub.Unsub()

	relay.Publish(ctx, reqEvent)

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
		return "", fmt.Errorf("no active bunker connection for user")
	}

	relay, err := nostr.RelayConnect(ctx, conn.RelayURL)
	if err != nil {
		return "", fmt.Errorf("failed to connect to relay: %w", err)
	}
	defer relay.Close()

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
	reqEvent.Sign(conn.ClientPrivateKey)

	sub, _ := relay.Subscribe(ctx, nostr.Filters{{
		Kinds:   []int{24133},
		Authors: []string{conn.BunkerPubkey},
		Tags:    nostr.TagMap{"p": []string{clientPubkey}},
		Since:   &reqEvent.CreatedAt,
	}})
	defer sub.Unsub()

	relay.Publish(ctx, reqEvent)

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
		return "", fmt.Errorf("no active bunker connection for user")
	}

	relay, err := nostr.RelayConnect(ctx, conn.RelayURL)
	if err != nil {
		return "", fmt.Errorf("failed to connect to relay: %w", err)
	}
	defer relay.Close()

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
	reqEvent.Sign(conn.ClientPrivateKey)

	sub, _ := relay.Subscribe(ctx, nostr.Filters{{
		Kinds:   []int{24133},
		Authors: []string{conn.BunkerPubkey},
		Tags:    nostr.TagMap{"p": []string{clientPubkey}},
		Since:   &reqEvent.CreatedAt,
	}})
	defer sub.Unsub()

	relay.Publish(ctx, reqEvent)

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
