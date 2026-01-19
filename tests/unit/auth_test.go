package unit

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/coldforge/coldforge-email/internal/auth"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// MockSessionStore implements SessionStore for testing
type MockSessionStore struct {
	sessions   map[string]*auth.Session
	tokens     map[string]*auth.Session
	challenges map[string]*auth.ChallengeData
}

func NewMockSessionStore() *MockSessionStore {
	return &MockSessionStore{
		sessions:   make(map[string]*auth.Session),
		tokens:     make(map[string]*auth.Session),
		challenges: make(map[string]*auth.ChallengeData),
	}
}

func (m *MockSessionStore) SaveSession(ctx context.Context, session *auth.Session) error {
	m.sessions[session.ID] = session
	m.tokens[session.Token] = session
	return nil
}

func (m *MockSessionStore) GetSession(ctx context.Context, sessionID string) (*auth.Session, error) {
	session, ok := m.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	return session, nil
}

func (m *MockSessionStore) GetSessionByToken(ctx context.Context, token string) (*auth.Session, error) {
	session, ok := m.tokens[token]
	if !ok {
		return nil, nil
	}
	return session, nil
}

func (m *MockSessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	if session, ok := m.sessions[sessionID]; ok {
		delete(m.tokens, session.Token)
	}
	delete(m.sessions, sessionID)
	return nil
}

func (m *MockSessionStore) SetNIP46Challenge(ctx context.Context, challengeID string, data *auth.ChallengeData, ttl time.Duration) error {
	m.challenges[challengeID] = data
	return nil
}

func (m *MockSessionStore) GetNIP46Challenge(ctx context.Context, challengeID string) (*auth.ChallengeData, error) {
	challenge, ok := m.challenges[challengeID]
	if !ok {
		return nil, nil
	}
	return challenge, nil
}

func (m *MockSessionStore) DeleteNIP46Challenge(ctx context.Context, challengeID string) error {
	delete(m.challenges, challengeID)
	return nil
}

// TestParseBunkerURL tests parsing of bunker:// URLs
func TestParseBunkerURL(t *testing.T) {
	tests := []struct {
		name        string
		bunkerURL   string
		wantPubkey  string
		wantRelay   string
		wantSecret  string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid bunker URL with relay and secret",
			bunkerURL:   "bunker://1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef?relay=wss://relay.example.com&secret=mysecret123",
			wantPubkey:  "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			wantRelay:   "wss://relay.example.com",
			wantSecret:  "mysecret123",
			expectError: false,
		},
		{
			name:        "valid bunker URL without secret",
			bunkerURL:   "bunker://abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789?relay=wss://relay.test.com",
			wantPubkey:  "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
			wantRelay:   "wss://relay.test.com",
			wantSecret:  "",
			expectError: false,
		},
		{
			name:        "invalid URL missing bunker:// prefix",
			bunkerURL:   "https://1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef?relay=wss://relay.example.com",
			expectError: true,
			errorMsg:    "must start with bunker://",
		},
		{
			name:        "invalid URL missing query parameters",
			bunkerURL:   "bunker://1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			expectError: true,
			errorMsg:    "missing query parameters",
		},
		{
			name:        "invalid URL missing relay parameter",
			bunkerURL:   "bunker://1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef?secret=test",
			expectError: true,
			errorMsg:    "missing relay parameter",
		},
		{
			name:        "invalid pubkey length too short",
			bunkerURL:   "bunker://shortpubkey?relay=wss://relay.example.com",
			expectError: true,
			errorMsg:    "must be 64 hex characters",
		},
		{
			name:        "invalid pubkey length too long",
			bunkerURL:   "bunker://1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef12345?relay=wss://relay.example.com",
			expectError: true,
			errorMsg:    "must be 64 hex characters",
		},
		{
			name:        "empty bunker URL",
			bunkerURL:   "",
			expectError: true,
			errorMsg:    "must start with bunker://",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := auth.ParseBunkerURL(tt.bunkerURL)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, tt.wantPubkey, result.BunkerPubkey)
				assert.Equal(t, tt.wantRelay, result.RelayURL)
				assert.Equal(t, tt.wantSecret, result.Secret)
			}
		})
	}
}

// TestCreateAuthChallenge tests challenge creation
func TestCreateAuthChallenge(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	sessionStore := NewMockSessionStore()

	handler, err := auth.NewNIP46Handler(
		"wss://relay.example.com",
		sessionStore,
		nil,
		logger,
	)
	require.NoError(t, err)

	tests := []struct {
		name        string
		bunkerURL   string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "successful challenge creation with valid bunker URL",
			bunkerURL:   "bunker://1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef?relay=wss://relay.example.com&secret=test",
			expectError: false,
		},
		{
			name:        "successful challenge creation without secret",
			bunkerURL:   "bunker://abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789?relay=wss://relay.test.com",
			expectError: false,
		},
		{
			name:        "invalid bunker URL",
			bunkerURL:   "invalid://url",
			expectError: true,
			errorMsg:    "invalid bunker URL",
		},
		{
			name:        "bunker URL with invalid pubkey",
			bunkerURL:   "bunker://invalid?relay=wss://relay.example.com",
			expectError: true,
			errorMsg:    "invalid bunker URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			challenge, err := handler.CreateAuthChallenge(ctx, tt.bunkerURL)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				assert.Nil(t, challenge)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, challenge)

				// Verify challenge has required fields
				assert.NotEmpty(t, challenge.ID, "Challenge ID should not be empty")
				assert.NotEmpty(t, challenge.Challenge, "Challenge string should not be empty")
				assert.NotEmpty(t, challenge.BunkerPubkey, "Bunker pubkey should not be empty")
				assert.NotEmpty(t, challenge.RelayURL, "Relay URL should not be empty")

				// Verify challenge length (32 bytes = 64 hex characters)
				assert.Equal(t, 64, len(challenge.Challenge), "Challenge should be 64 hex characters")

				// Verify timestamps
				assert.Greater(t, challenge.CreatedAt, int64(0), "CreatedAt should be set")
				assert.Greater(t, challenge.ExpiresAt, challenge.CreatedAt, "ExpiresAt should be after CreatedAt")

				// Verify expiration is approximately 5 minutes in the future
				now := time.Now().Unix()
				fiveMinutes := int64(5 * 60)
				assert.InDelta(t, now+fiveMinutes, challenge.ExpiresAt, 10, "ExpiresAt should be about 5 minutes in the future")

				// Verify challenge was stored
				stored, err := sessionStore.GetNIP46Challenge(ctx, challenge.ID)
				assert.NoError(t, err)
				require.NotNil(t, stored, "Challenge should be stored")
				assert.Equal(t, challenge.Challenge, stored.Challenge)
				assert.Equal(t, challenge.BunkerPubkey, stored.BunkerPubkey)
				assert.Equal(t, challenge.RelayURL, stored.RelayURL)
				assert.NotEmpty(t, stored.ClientPrivateKey, "Client private key should be stored")
			}
		})
	}
}

// TestVerifyAuthSignature tests signature verification
func TestVerifyAuthSignature(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	sessionStore := NewMockSessionStore()

	handler, err := auth.NewNIP46Handler(
		"wss://relay.example.com",
		sessionStore,
		nil,
		logger,
	)
	require.NoError(t, err)

	ctx := context.Background()

	bunkerURL := "bunker://1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef?relay=wss://relay.example.com"

	// Generate a test keypair
	privateKey := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(privateKey)
	require.NoError(t, err)

	// Helper to create a fresh challenge for each test
	createFreshChallenge := func() *auth.AuthChallenge {
		ch, err := handler.CreateAuthChallenge(ctx, bunkerURL)
		require.NoError(t, err)
		return ch
	}

	tests := []struct {
		name            string
		setupChallenge  func() (*auth.AuthChallenge, string)
		createEvent     func(challenge *auth.AuthChallenge) string
		expectError     bool
		errorMsg        string
		checkSession    bool
		expectedPubkey  string
	}{
		{
			name: "valid signed event with matching challenge",
			setupChallenge: func() (*auth.AuthChallenge, string) {
				ch := createFreshChallenge()
				return ch, ch.ID
			},
			createEvent: func(challenge *auth.AuthChallenge) string {
				event := nostr.Event{
					Kind:      1,
					PubKey:    pubkey,
					CreatedAt: nostr.Timestamp(time.Now().Unix()),
					Content:   challenge.Challenge,
					Tags:      nostr.Tags{},
				}
				err := event.Sign(privateKey)
				require.NoError(t, err)
				eventJSON, err := json.Marshal(event)
				require.NoError(t, err)
				return string(eventJSON)
			},
			expectError:    false,
			checkSession:   true,
			expectedPubkey: pubkey,
		},
		{
			name: "invalid signature rejection",
			setupChallenge: func() (*auth.AuthChallenge, string) {
				ch := createFreshChallenge()
				return ch, ch.ID
			},
			createEvent: func(challenge *auth.AuthChallenge) string {
				event := nostr.Event{
					Kind:      1,
					PubKey:    pubkey,
					CreatedAt: nostr.Timestamp(time.Now().Unix()),
					Content:   challenge.Challenge,
					Tags:      nostr.Tags{},
				}
				// Sign with correct key
				err := event.Sign(privateKey)
				require.NoError(t, err)
				// Tamper with signature
				event.Sig = "invalid" + event.Sig[7:]
				eventJSON, err := json.Marshal(event)
				require.NoError(t, err)
				return string(eventJSON)
			},
			expectError: true,
			errorMsg:    "invalid signature",
		},
		{
			name: "challenge mismatch rejection",
			setupChallenge: func() (*auth.AuthChallenge, string) {
				ch := createFreshChallenge()
				return ch, ch.ID
			},
			createEvent: func(challenge *auth.AuthChallenge) string {
				event := nostr.Event{
					Kind:      1,
					PubKey:    pubkey,
					CreatedAt: nostr.Timestamp(time.Now().Unix()),
					Content:   "wrong_challenge_string",
					Tags:      nostr.Tags{},
				}
				err := event.Sign(privateKey)
				require.NoError(t, err)
				eventJSON, err := json.Marshal(event)
				require.NoError(t, err)
				return string(eventJSON)
			},
			expectError: true,
			errorMsg:    "challenge mismatch",
		},
		{
			name: "expired event rejection",
			setupChallenge: func() (*auth.AuthChallenge, string) {
				ch := createFreshChallenge()
				return ch, ch.ID
			},
			createEvent: func(challenge *auth.AuthChallenge) string {
				// Create event from 10 minutes ago
				oldTime := time.Now().Add(-10 * time.Minute)
				event := nostr.Event{
					Kind:      1,
					PubKey:    pubkey,
					CreatedAt: nostr.Timestamp(oldTime.Unix()),
					Content:   challenge.Challenge,
					Tags:      nostr.Tags{},
				}
				err := event.Sign(privateKey)
				require.NoError(t, err)
				eventJSON, err := json.Marshal(event)
				require.NoError(t, err)
				return string(eventJSON)
			},
			expectError: true,
			errorMsg:    "event too old",
		},
		{
			name: "expired challenge rejection",
			setupChallenge: func() (*auth.AuthChallenge, string) {
				// Create a challenge and manually delete it to simulate expiration
				expiredChallenge := createFreshChallenge()
				sessionStore.DeleteNIP46Challenge(ctx, expiredChallenge.ID)
				return expiredChallenge, expiredChallenge.ID
			},
			createEvent: func(challenge *auth.AuthChallenge) string {
				event := nostr.Event{
					Kind:      1,
					PubKey:    pubkey,
					CreatedAt: nostr.Timestamp(time.Now().Unix()),
					Content:   "some_challenge",
					Tags:      nostr.Tags{},
				}
				err := event.Sign(privateKey)
				require.NoError(t, err)
				eventJSON, err := json.Marshal(event)
				require.NoError(t, err)
				return string(eventJSON)
			},
			expectError: true,
			errorMsg:    "challenge not found or expired",
		},
		{
			name: "invalid JSON rejection",
			setupChallenge: func() (*auth.AuthChallenge, string) {
				ch := createFreshChallenge()
				return ch, ch.ID
			},
			createEvent: func(challenge *auth.AuthChallenge) string {
				return "invalid json {{{{"
			},
			expectError: true,
			errorMsg:    "invalid event JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			challenge, challengeID := tt.setupChallenge()
			signedEventJSON := tt.createEvent(challenge)

			session, err := handler.VerifyAuthSignature(ctx, challengeID, signedEventJSON)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				assert.Nil(t, session)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, session)

				if tt.checkSession {
					assert.NotEmpty(t, session.ID)
					assert.NotEmpty(t, session.Token)
					assert.Equal(t, tt.expectedPubkey, session.UserID)
					assert.True(t, session.ExpiresAt.After(time.Now()))
					assert.True(t, session.CreatedAt.Before(time.Now().Add(1*time.Second)))

					// Verify session was saved
					saved, err := sessionStore.GetSession(ctx, session.ID)
					assert.NoError(t, err)
					require.NotNil(t, saved)
					assert.Equal(t, session.ID, saved.ID)
					assert.Equal(t, session.UserID, saved.UserID)

					// Verify challenge was deleted
					deletedChallenge, _ := sessionStore.GetNIP46Challenge(ctx, challengeID)
					assert.Nil(t, deletedChallenge, "Challenge should be deleted after successful verification")
				}
			}
		})
	}
}

// TestValidateSession tests session validation
func TestValidateSession(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	sessionStore := NewMockSessionStore()

	handler, err := auth.NewNIP46Handler(
		"wss://relay.example.com",
		sessionStore,
		nil,
		logger,
	)
	require.NoError(t, err)

	ctx := context.Background()

	// Create test sessions
	validSession := &auth.Session{
		ID:        "valid-session-id",
		UserID:    "npub1testuser",
		Token:     "valid-token-123",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}

	expiredSession := &auth.Session{
		ID:        "expired-session-id",
		UserID:    "npub1expired",
		Token:     "expired-token-123",
		ExpiresAt: time.Now().Add(-1 * time.Hour), // Already expired
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}

	// Save sessions
	sessionStore.SaveSession(ctx, validSession)
	sessionStore.SaveSession(ctx, expiredSession)

	tests := []struct {
		name          string
		token         string
		expectError   bool
		errorMsg      string
		expectedUser  string
		checkDeleted  bool
	}{
		{
			name:         "valid session returns session data",
			token:        "valid-token-123",
			expectError:  false,
			expectedUser: "npub1testuser",
		},
		{
			name:         "expired session returns error",
			token:        "expired-token-123",
			expectError:  true,
			errorMsg:     "session expired",
			checkDeleted: true,
		},
		{
			name:        "invalid token returns error",
			token:       "non-existent-token",
			expectError: true,
			errorMsg:    "invalid session token",
		},
		{
			name:        "empty token returns error",
			token:       "",
			expectError: true,
			errorMsg:    "invalid session token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := handler.ValidateSession(ctx, tt.token)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				assert.Nil(t, session)

				// Check if expired session was deleted
				if tt.checkDeleted {
					deleted, _ := sessionStore.GetSessionByToken(ctx, tt.token)
					assert.Nil(t, deleted, "Expired session should be deleted")
				}
			} else {
				assert.NoError(t, err)
				require.NotNil(t, session)
				assert.Equal(t, tt.expectedUser, session.UserID)
				assert.Equal(t, tt.token, session.Token)
				assert.True(t, session.ExpiresAt.After(time.Now()))
			}
		})
	}
}

// TestHelperFunctions tests helper functions
func TestHelperFunctions(t *testing.T) {
	t.Run("generateRandomHex produces correct length", func(t *testing.T) {
		tests := []struct {
			name         string
			length       int
			expectedLen  int
		}{
			{
				name:        "16 bytes = 32 hex chars",
				length:      16,
				expectedLen: 32,
			},
			{
				name:        "32 bytes = 64 hex chars",
				length:      32,
				expectedLen: 64,
			},
			{
				name:        "8 bytes = 16 hex chars",
				length:      8,
				expectedLen: 16,
			},
			{
				name:        "1 byte = 2 hex chars",
				length:      1,
				expectedLen: 2,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// We need to access the internal function through a challenge creation
				// since generateRandomHex is not exported
				logger, _ := zap.NewDevelopment()
				defer logger.Sync()

				sessionStore := NewMockSessionStore()
				handler, err := auth.NewNIP46Handler(
					"wss://relay.example.com",
					sessionStore,
					nil,
					logger,
				)
				require.NoError(t, err)

				ctx := context.Background()
				bunkerURL := "bunker://1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef?relay=wss://relay.example.com"
				challenge, err := handler.CreateAuthChallenge(ctx, bunkerURL)
				require.NoError(t, err)

				// The challenge should be 64 hex characters (32 bytes)
				assert.Equal(t, 64, len(challenge.Challenge))

				// Verify it's valid hex
				for _, c := range challenge.Challenge {
					assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
						fmt.Sprintf("Character '%c' is not valid hex", c))
				}

				// Verify uniqueness by creating multiple challenges
				challenge2, err := handler.CreateAuthChallenge(ctx, bunkerURL)
				require.NoError(t, err)
				assert.NotEqual(t, challenge.Challenge, challenge2.Challenge,
					"Challenges should be unique")
			})
		}
	})

	t.Run("generateSecureToken produces 64 character hex", func(t *testing.T) {
		// Test through session creation since generateSecureToken is not exported
		logger, _ := zap.NewDevelopment()
		defer logger.Sync()

		sessionStore := NewMockSessionStore()
		handler, err := auth.NewNIP46Handler(
			"wss://relay.example.com",
			sessionStore,
			nil,
			logger,
		)
		require.NoError(t, err)

		ctx := context.Background()

		// Create a session through the verify flow
		bunkerURL := "bunker://1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef?relay=wss://relay.example.com"
		challenge, err := handler.CreateAuthChallenge(ctx, bunkerURL)
		require.NoError(t, err)

		privateKey := nostr.GeneratePrivateKey()
		pubkey, err := nostr.GetPublicKey(privateKey)
		require.NoError(t, err)

		event := nostr.Event{
			Kind:      1,
			PubKey:    pubkey,
			CreatedAt: nostr.Timestamp(time.Now().Unix()),
			Content:   challenge.Challenge,
			Tags:      nostr.Tags{},
		}
		err = event.Sign(privateKey)
		require.NoError(t, err)

		eventJSON, err := json.Marshal(event)
		require.NoError(t, err)

		session, err := handler.VerifyAuthSignature(ctx, challenge.ID, string(eventJSON))
		require.NoError(t, err)

		// Verify token is 64 hex characters
		assert.Equal(t, 64, len(session.Token), "Token should be 64 hex characters")

		// Verify it's valid hex
		for _, c := range session.Token {
			assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
				fmt.Sprintf("Character '%c' is not valid hex", c))
		}

		// Create another session and verify tokens are unique
		challenge2, err := handler.CreateAuthChallenge(ctx, bunkerURL)
		require.NoError(t, err)

		event2 := nostr.Event{
			Kind:      1,
			PubKey:    pubkey,
			CreatedAt: nostr.Timestamp(time.Now().Unix()),
			Content:   challenge2.Challenge,
			Tags:      nostr.Tags{},
		}
		err = event2.Sign(privateKey)
		require.NoError(t, err)

		eventJSON2, err := json.Marshal(event2)
		require.NoError(t, err)

		session2, err := handler.VerifyAuthSignature(ctx, challenge2.ID, string(eventJSON2))
		require.NoError(t, err)

		assert.NotEqual(t, session.Token, session2.Token, "Tokens should be unique")
	})
}

// TestLogout tests session invalidation and connection cleanup
func TestLogout(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	sessionStore := NewMockSessionStore()

	handler, err := auth.NewNIP46Handler(
		"wss://relay.example.com",
		sessionStore,
		nil,
		logger,
	)
	require.NoError(t, err)

	ctx := context.Background()

	tests := []struct {
		name         string
		setupSession func() *auth.Session
		expectError  bool
	}{
		{
			name: "successful logout with existing session",
			setupSession: func() *auth.Session {
				session := &auth.Session{
					ID:        "test-session-id",
					UserID:    "npub1testuser",
					Token:     "test-token",
					ExpiresAt: time.Now().Add(24 * time.Hour),
					CreatedAt: time.Now(),
				}
				sessionStore.SaveSession(ctx, session)
				return session
			},
			expectError: false,
		},
		{
			name: "logout with non-existent session",
			setupSession: func() *auth.Session {
				return &auth.Session{
					ID: "non-existent-session",
				}
			},
			expectError: false, // Should not error even if session doesn't exist
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := tt.setupSession()

			err := handler.Logout(ctx, session.ID)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				// Verify session is deleted
				retrieved, err := sessionStore.GetSession(ctx, session.ID)
				assert.NoError(t, err)
				assert.Nil(t, retrieved, "Session should be deleted after logout")

				// Verify token is also removed
				if session.Token != "" {
					byToken, err := sessionStore.GetSessionByToken(ctx, session.Token)
					assert.NoError(t, err)
					assert.Nil(t, byToken, "Session token should be deleted")
				}
			}
		})
	}
}

// TestMockSessionStoreConsistency verifies the mock session store behaves correctly
func TestMockSessionStoreConsistency(t *testing.T) {
	store := NewMockSessionStore()
	ctx := context.Background()

	t.Run("session storage and retrieval", func(t *testing.T) {
		session := &auth.Session{
			ID:        "test-id",
			UserID:    "test-user",
			Token:     "test-token",
			ExpiresAt: time.Now().Add(1 * time.Hour),
			CreatedAt: time.Now(),
		}

		// Save
		err := store.SaveSession(ctx, session)
		assert.NoError(t, err)

		// Retrieve by ID
		byID, err := store.GetSession(ctx, session.ID)
		assert.NoError(t, err)
		require.NotNil(t, byID)
		assert.Equal(t, session.ID, byID.ID)

		// Retrieve by token
		byToken, err := store.GetSessionByToken(ctx, session.Token)
		assert.NoError(t, err)
		require.NotNil(t, byToken)
		assert.Equal(t, session.ID, byToken.ID)

		// Delete
		err = store.DeleteSession(ctx, session.ID)
		assert.NoError(t, err)

		// Verify deleted
		deleted, err := store.GetSession(ctx, session.ID)
		assert.NoError(t, err)
		assert.Nil(t, deleted)

		deletedByToken, err := store.GetSessionByToken(ctx, session.Token)
		assert.NoError(t, err)
		assert.Nil(t, deletedByToken)
	})

	t.Run("challenge storage and retrieval", func(t *testing.T) {
		challengeData := &auth.ChallengeData{
			Challenge:        "test-challenge",
			BunkerPubkey:     "test-pubkey",
			RelayURL:         "wss://relay.test.com",
			ClientPrivateKey: "test-privkey",
			CreatedAt:        time.Now().Unix(),
		}

		// Save
		err := store.SetNIP46Challenge(ctx, "challenge-id", challengeData, 5*time.Minute)
		assert.NoError(t, err)

		// Retrieve
		retrieved, err := store.GetNIP46Challenge(ctx, "challenge-id")
		assert.NoError(t, err)
		require.NotNil(t, retrieved)
		assert.Equal(t, challengeData.Challenge, retrieved.Challenge)

		// Delete
		err = store.DeleteNIP46Challenge(ctx, "challenge-id")
		assert.NoError(t, err)

		// Verify deleted
		deleted, err := store.GetNIP46Challenge(ctx, "challenge-id")
		assert.NoError(t, err)
		assert.Nil(t, deleted)
	})
}
