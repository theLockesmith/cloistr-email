package abuse

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Mark is the ladder's current standing judgement about an account.
//
// Marks live in Redis rather than a mailboxes column on purpose: every rung
// below hold is meant to expire on its own. A throttle that needs a human to
// lift it is just a slow suspend, and a schema column would outlive the
// behaviour that caused it.
type Mark struct {
	// Rung is the applied severity.
	Rung Rung `json:"rung"`

	// Reason is the human-readable trigger, carried so an operator reviewing a
	// throttle does not have to re-derive it from the raw counters.
	Reason string `json:"reason"`

	// At is when the mark was applied.
	At time.Time `json:"at"`
}

// MarkStore persists ladder marks across replicas.
type MarkStore interface {
	// Get returns the current mark, if any.
	Get(ctx context.Context, pubkey string) (Mark, bool, error)

	// Set writes a mark, expiring after ttl. A ttl of zero means no expiry,
	// which is only appropriate for hold and suspend.
	Set(ctx context.Context, pubkey string, m Mark, ttl time.Duration) error

	// Clear removes the mark, de-escalating the account to RungNone.
	Clear(ctx context.Context, pubkey string) error
}

func markKey(pubkey string) string { return "abuse:mark:" + pubkey }

// RedisMarkStore is the production MarkStore, shared by every backend replica so
// that a throttle applied by one pod is honoured by all of them.
type RedisMarkStore struct{ client *redis.Client }

// NewRedisMarkStore wraps a go-redis client.
func NewRedisMarkStore(client *redis.Client) *RedisMarkStore {
	return &RedisMarkStore{client: client}
}

func (s *RedisMarkStore) Get(ctx context.Context, pubkey string) (Mark, bool, error) {
	raw, err := s.client.Get(ctx, markKey(pubkey)).Bytes()
	if err == redis.Nil {
		return Mark{}, false, nil
	}
	if err != nil {
		return Mark{}, false, fmt.Errorf("abuse mark get: %w", err)
	}

	var m Mark
	if err := json.Unmarshal(raw, &m); err != nil {
		// A malformed mark must not wedge the send path into permanent denial.
		return Mark{}, false, fmt.Errorf("abuse mark decode: %w", err)
	}
	return m, true, nil
}

func (s *RedisMarkStore) Set(ctx context.Context, pubkey string, m Mark, ttl time.Duration) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("abuse mark encode: %w", err)
	}
	if err := s.client.Set(ctx, markKey(pubkey), raw, ttl).Err(); err != nil {
		return fmt.Errorf("abuse mark set: %w", err)
	}
	return nil
}

func (s *RedisMarkStore) Clear(ctx context.Context, pubkey string) error {
	if err := s.client.Del(ctx, markKey(pubkey)).Err(); err != nil {
		return fmt.Errorf("abuse mark clear: %w", err)
	}
	return nil
}

// Throttled reports whether the ladder has clamped this account's limits. The
// send gate calls this on every send, so it must stay a single key read.
//
// A lookup error returns false: a Redis blip must not turn into a service-wide
// send outage. The gate's own tier and rate-limit checks still apply, and the
// harder rungs (hold, suspend) are enforced in Postgres rather than here.
func (s *RedisMarkStore) Throttled(ctx context.Context, pubkey string) (bool, error) {
	m, ok, err := s.Get(ctx, pubkey)
	if err != nil || !ok {
		return false, err
	}
	return m.Rung >= RungThrottle, nil
}

// MemoryMarkStore is an in-process MarkStore for tests and single-node
// self-hosting, where there is no second replica to coordinate with.
type MemoryMarkStore struct {
	mu    sync.RWMutex
	marks map[string]memoryMark
	now   func() time.Time
}

type memoryMark struct {
	mark      Mark
	expiresAt time.Time // zero means no expiry
}

// NewMemoryMarkStore creates an empty in-memory store.
func NewMemoryMarkStore() *MemoryMarkStore {
	return &MemoryMarkStore{marks: make(map[string]memoryMark), now: time.Now}
}

func (s *MemoryMarkStore) Get(_ context.Context, pubkey string) (Mark, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.marks[pubkey]
	if !ok {
		return Mark{}, false, nil
	}
	if !entry.expiresAt.IsZero() && s.now().After(entry.expiresAt) {
		return Mark{}, false, nil
	}
	return entry.mark, true, nil
}

func (s *MemoryMarkStore) Set(_ context.Context, pubkey string, m Mark, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := memoryMark{mark: m}
	if ttl > 0 {
		entry.expiresAt = s.now().Add(ttl)
	}
	s.marks[pubkey] = entry
	return nil
}

func (s *MemoryMarkStore) Clear(_ context.Context, pubkey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.marks, pubkey)
	return nil
}

// Throttled mirrors RedisMarkStore.Throttled.
func (s *MemoryMarkStore) Throttled(ctx context.Context, pubkey string) (bool, error) {
	m, ok, err := s.Get(ctx, pubkey)
	if err != nil || !ok {
		return false, err
	}
	return m.Rung >= RungThrottle, nil
}
