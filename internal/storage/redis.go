package storage

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RedisSessionStore manages sessions in Redis
type RedisSessionStore struct {
	client *redis.Client
	logger *zap.Logger
	ttl    time.Duration
}

// NewRedisSessionStore creates a new Redis session store
func NewRedisSessionStore(redisURL string, logger *zap.Logger) (*RedisSessionStore, error) {
	logger.Info("Initializing Redis session store", zap.String("url", redisURL))

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opt)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}

	return &RedisSessionStore{
		client: client,
		logger: logger,
		ttl:    24 * time.Hour, // Default session TTL
	}, nil
}

// Session represents a user session
type Session struct {
	ID        string
	UserID    string // Nostr npub
	Email     string
	Token     string
	ExpiresAt time.Time
}

// SaveSession saves a session to Redis
func (s *RedisSessionStore) SaveSession(ctx context.Context, session *Session) error {
	s.logger.Debug("Saving session", zap.String("id", session.ID), zap.String("user_id", session.UserID))

	// Store as JSON with TTL
	// Actual implementation would serialize and store
	ttl := time.Until(session.ExpiresAt)
	if ttl < 0 {
		ttl = s.ttl
	}

	key := "session:" + session.ID
	// Stub: actual implementation would use JSON marshaling
	if err := s.client.Set(ctx, key, "", ttl).Err(); err != nil {
		return err
	}

	return nil
}

// GetSession retrieves a session from Redis
func (s *RedisSessionStore) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	s.logger.Debug("Getting session", zap.String("id", sessionID))

	key := "session:" + sessionID
	val, err := s.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil // Session not found
	}
	if err != nil {
		return nil, err
	}

	// Stub: actual implementation would unmarshal from JSON
	_ = val

	return nil, nil
}

// DeleteSession removes a session from Redis
func (s *RedisSessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	s.logger.Debug("Deleting session", zap.String("id", sessionID))

	key := "session:" + sessionID
	return s.client.Del(ctx, key).Err()
}

// SetNIP46Challenge stores a NIP-46 challenge
func (s *RedisSessionStore) SetNIP46Challenge(ctx context.Context, challengeID string, challenge string, ttl time.Duration) error {
	s.logger.Debug("Setting NIP-46 challenge", zap.String("challenge_id", challengeID))

	key := "nip46:challenge:" + challengeID
	return s.client.Set(ctx, key, challenge, ttl).Err()
}

// GetNIP46Challenge retrieves a NIP-46 challenge
func (s *RedisSessionStore) GetNIP46Challenge(ctx context.Context, challengeID string) (string, error) {
	s.logger.Debug("Getting NIP-46 challenge", zap.String("challenge_id", challengeID))

	key := "nip46:challenge:" + challengeID
	val, err := s.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil // Challenge not found
	}
	return val, err
}

// Close closes the Redis connection
func (s *RedisSessionStore) Close() error {
	s.logger.Info("Closing Redis connection")
	return s.client.Close()
}
