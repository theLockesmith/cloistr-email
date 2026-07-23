package ratelimit

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// incrByScript atomically increments and sets the TTL only on first write, so a
// long-lived key can't have its window silently extended by later increments.
var incrByScript = redis.NewScript(`
local v = redis.call('INCRBY', KEYS[1], ARGV[1])
if v == tonumber(ARGV[1]) then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return v
`)

// RedisStore is the production Store, backed by Redis/Dragonfly. Counters are
// shared by every backend replica, which is what makes the limits real rather
// than per-pod.
type RedisStore struct{ client *redis.Client }

// NewRedisStore wraps a go-redis client.
func NewRedisStore(client *redis.Client) *RedisStore { return &RedisStore{client: client} }

func (s *RedisStore) IncrBy(ctx context.Context, key string, n int64, ttl time.Duration) (int64, error) {
	return incrByScript.Run(ctx, s.client, []string{key}, n, ttl.Milliseconds()).Int64()
}

func (s *RedisStore) PeekAll(ctx context.Context, keys []string) (map[string]int64, error) {
	out := make(map[string]int64, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	vals, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, v := range vals {
		if v == nil {
			continue
		}
		if str, ok := v.(string); ok {
			var n int64
			for _, c := range str {
				if c < '0' || c > '9' {
					n = 0
					break
				}
				n = n*10 + int64(c-'0')
			}
			out[keys[i]] = n
		}
	}
	return out, nil
}

// MemoryStore is an in-process Store for tests and single-node/self-hosted
// deployments with no Redis. Expiry is evaluated lazily on access.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]memEntry
	now     func() time.Time
}

type memEntry struct {
	val       int64
	expiresAt time.Time
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[string]memEntry), now: time.Now}
}

func (s *MemoryStore) IncrBy(ctx context.Context, key string, n int64, ttl time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	e, ok := s.entries[key]
	if !ok || now.After(e.expiresAt) {
		e = memEntry{val: 0, expiresAt: now.Add(ttl)}
	}
	e.val += n
	s.entries[key] = e
	return e.val, nil
}

func (s *MemoryStore) PeekAll(ctx context.Context, keys []string) (map[string]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := make(map[string]int64, len(keys))
	for _, k := range keys {
		if e, ok := s.entries[k]; ok && !now.After(e.expiresAt) {
			out[k] = e.val
		}
	}
	return out, nil
}

var (
	_ Store = (*RedisStore)(nil)
	_ Store = (*MemoryStore)(nil)
)
