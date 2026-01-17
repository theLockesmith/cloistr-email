package unit

import (
	"context"
	"testing"
	"time"

	"github.com/coldforge/coldforge-email/internal/auth"
	"go.uber.org/zap"
)

// MockSessionStore implements SessionStore for testing
type MockSessionStore struct {
	sessions  map[string]*auth.Session
	challenges map[string]string
}

func NewMockSessionStore() *MockSessionStore {
	return &MockSessionStore{
		sessions:   make(map[string]*auth.Session),
		challenges: make(map[string]string),
	}
}

func (m *MockSessionStore) SaveSession(ctx context.Context, session *auth.Session) error {
	m.sessions[session.ID] = session
	return nil
}

func (m *MockSessionStore) GetSession(ctx context.Context, sessionID string) (*auth.Session, error) {
	return m.sessions[sessionID], nil
}

func (m *MockSessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	delete(m.sessions, sessionID)
	return nil
}

func (m *MockSessionStore) SetNIP46Challenge(ctx context.Context, challengeID string, challenge string, ttl time.Duration) error {
	m.challenges[challengeID] = challenge
	return nil
}

func (m *MockSessionStore) GetNIP46Challenge(ctx context.Context, challengeID string) (string, error) {
	return m.challenges[challengeID], nil
}

// TestCreateAuthChallenge tests challenge creation
func TestCreateAuthChallenge(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	sessionStore := NewMockSessionStore()

	handler, err := auth.NewNIP46Handler(
		"ws://localhost:4737",
		sessionStore,
		nil,
		logger,
	)
	if err != nil {
		t.Fatalf("Failed to create NIP46Handler: %v", err)
	}

	ctx := context.Background()
	challenge, err := handler.CreateAuthChallenge(ctx)
	if err != nil {
		t.Fatalf("Failed to create challenge: %v", err)
	}

	// Verify challenge has required fields
	if challenge.ID == "" {
		t.Error("Challenge ID is empty")
	}

	if challenge.Challenge == "" {
		t.Error("Challenge string is empty")
	}

	if challenge.ExpiresAt.Before(time.Now()) {
		t.Error("Challenge already expired")
	}

	// Verify challenge was stored
	stored, err := sessionStore.GetNIP46Challenge(ctx, challenge.ID)
	if err != nil {
		t.Fatalf("Failed to retrieve stored challenge: %v", err)
	}

	if stored != challenge.Challenge {
		t.Error("Stored challenge does not match")
	}
}

// TestLogout tests session invalidation
func TestLogout(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	sessionStore := NewMockSessionStore()

	handler, err := auth.NewNIP46Handler(
		"ws://localhost:4737",
		sessionStore,
		nil,
		logger,
	)
	if err != nil {
		t.Fatalf("Failed to create NIP46Handler: %v", err)
	}

	ctx := context.Background()

	// Create a session
	session := &auth.Session{
		ID:        "test-session-id",
		UserID:    "npub1test",
		Token:     "test-token",
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	err = sessionStore.SaveSession(ctx, session)
	if err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	// Logout
	err = handler.Logout(ctx, session.ID)
	if err != nil {
		t.Fatalf("Logout failed: %v", err)
	}

	// Verify session is deleted
	retrieved, err := sessionStore.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("Failed to retrieve session: %v", err)
	}

	if retrieved != nil {
		t.Error("Session should be deleted")
	}
}
