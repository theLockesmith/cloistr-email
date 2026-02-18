package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/auth"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RedisSessionStore manages sessions in Redis
// Implements auth.SessionStore interface
type RedisSessionStore struct {
	client *redis.Client
	logger *zap.Logger
	ttl    time.Duration
}

// Ensure RedisSessionStore implements auth.SessionStore
var _ auth.SessionStore = (*RedisSessionStore)(nil)

// NewRedisSessionStore creates a new Redis session store
func NewRedisSessionStore(redisURL string, logger *zap.Logger) (*RedisSessionStore, error) {
	logger.Info("Initializing Redis session store", zap.String("url", redisURL))

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
	}

	client := redis.NewClient(opt)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	logger.Info("Redis connection established")

	return &RedisSessionStore{
		client: client,
		logger: logger,
		ttl:    24 * time.Hour, // Default session TTL
	}, nil
}

// Key prefixes
const (
	sessionPrefix       = "session:"
	sessionTokenPrefix  = "session:token:"
	nip46ChallengePrefix = "nip46:challenge:"
)

// SaveSession saves a session to Redis
func (s *RedisSessionStore) SaveSession(ctx context.Context, session *auth.Session) error {
	s.logger.Debug("Saving session",
		zap.String("id", session.ID),
		zap.String("user_id", session.UserID))

	// Serialize session to JSON
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	// Calculate TTL from expiration
	ttl := time.Until(session.ExpiresAt)
	if ttl < 0 {
		ttl = s.ttl
	}

	// Store session by ID
	sessionKey := sessionPrefix + session.ID
	if err := s.client.Set(ctx, sessionKey, data, ttl).Err(); err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	// Also index by token for token-based lookup
	tokenKey := sessionTokenPrefix + session.Token
	if err := s.client.Set(ctx, tokenKey, session.ID, ttl).Err(); err != nil {
		// Clean up the session key if token index fails
		s.client.Del(ctx, sessionKey)
		return fmt.Errorf("failed to save session token index: %w", err)
	}

	s.logger.Debug("Session saved successfully", zap.String("id", session.ID))
	return nil
}

// GetSession retrieves a session from Redis by ID
func (s *RedisSessionStore) GetSession(ctx context.Context, sessionID string) (*auth.Session, error) {
	s.logger.Debug("Getting session", zap.String("id", sessionID))

	key := sessionPrefix + sessionID
	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil // Session not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	var session auth.Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session: %w", err)
	}

	return &session, nil
}

// GetSessionByToken retrieves a session from Redis by token
func (s *RedisSessionStore) GetSessionByToken(ctx context.Context, token string) (*auth.Session, error) {
	s.logger.Debug("Getting session by token")

	// Look up session ID by token
	tokenKey := sessionTokenPrefix + token
	sessionID, err := s.client.Get(ctx, tokenKey).Result()
	if err == redis.Nil {
		return nil, nil // Token not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session by token: %w", err)
	}

	// Get the actual session
	return s.GetSession(ctx, sessionID)
}

// DeleteSession removes a session from Redis
func (s *RedisSessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	s.logger.Debug("Deleting session", zap.String("id", sessionID))

	// First get the session to find the token
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}

	// Delete both keys
	sessionKey := sessionPrefix + sessionID
	if err := s.client.Del(ctx, sessionKey).Err(); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	// Delete token index if session was found
	if session != nil && session.Token != "" {
		tokenKey := sessionTokenPrefix + session.Token
		s.client.Del(ctx, tokenKey) // Ignore error for token cleanup
	}

	s.logger.Debug("Session deleted successfully", zap.String("id", sessionID))
	return nil
}

// SetNIP46Challenge stores a NIP-46 challenge with associated data
func (s *RedisSessionStore) SetNIP46Challenge(ctx context.Context, challengeID string, data *auth.ChallengeData, ttl time.Duration) error {
	s.logger.Debug("Setting NIP-46 challenge", zap.String("challenge_id", challengeID))

	// Serialize challenge data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal challenge data: %w", err)
	}

	key := nip46ChallengePrefix + challengeID
	if err := s.client.Set(ctx, key, jsonData, ttl).Err(); err != nil {
		return fmt.Errorf("failed to save challenge: %w", err)
	}

	s.logger.Debug("Challenge saved successfully", zap.String("challenge_id", challengeID))
	return nil
}

// GetNIP46Challenge retrieves a NIP-46 challenge
func (s *RedisSessionStore) GetNIP46Challenge(ctx context.Context, challengeID string) (*auth.ChallengeData, error) {
	s.logger.Debug("Getting NIP-46 challenge", zap.String("challenge_id", challengeID))

	key := nip46ChallengePrefix + challengeID
	data, err := s.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil // Challenge not found
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get challenge: %w", err)
	}

	var challengeData auth.ChallengeData
	if err := json.Unmarshal(data, &challengeData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal challenge data: %w", err)
	}

	return &challengeData, nil
}

// DeleteNIP46Challenge removes a NIP-46 challenge from Redis
func (s *RedisSessionStore) DeleteNIP46Challenge(ctx context.Context, challengeID string) error {
	s.logger.Debug("Deleting NIP-46 challenge", zap.String("challenge_id", challengeID))

	key := nip46ChallengePrefix + challengeID
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to delete challenge: %w", err)
	}

	return nil
}

// Health checks if Redis is healthy
func (s *RedisSessionStore) Health(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

// Close closes the Redis connection
func (s *RedisSessionStore) Close() error {
	s.logger.Info("Closing Redis connection")
	return s.client.Close()
}

// GetClient returns the underlying Redis client for advanced operations
func (s *RedisSessionStore) GetClient() *redis.Client {
	return s.client
}
