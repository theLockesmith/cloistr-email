package ratelimit

import (
	"context"
	"time"
)

// Default per-account key-discovery limits. Each lookup can be an outbound
// NIP-05 fetch on the caller's behalf; a person composing mail needs a handful
// a minute, a script enumerating addresses needs thousands.
const (
	DiscoverPerMinute = 30
	DiscoverPerHour   = 300
	DiscoverPerDay    = 2000
)

// NewDiscoverLimiter returns a per-account limiter for key discovery, built on
// the send limiter. Its counters live under their own key prefix, so lookups
// never consume an account's send quota. A zero limit disables that window.
func NewDiscoverLimiter(store Store, perMin, perHour, perDay int) *Limiter {
	return New(prefixStore{inner: store, prefix: "discover:"}, Limits{
		Account: AccountLimits{
			MsgPerMin:  Window{Max: perMin, Period: time.Minute},
			MsgPerHour: Window{Max: perHour, Period: time.Hour},
			MsgPerDay:  Window{Max: perDay, Period: 24 * time.Hour},
		},
	})
}

type prefixStore struct {
	inner  Store
	prefix string
}

func (p prefixStore) IncrBy(ctx context.Context, key string, n int64, ttl time.Duration) (int64, error) {
	return p.inner.IncrBy(ctx, p.prefix+key, n, ttl)
}

func (p prefixStore) PeekAll(ctx context.Context, keys []string) (map[string]int64, error) {
	prefixed := make([]string, len(keys))
	for i, k := range keys {
		prefixed[i] = p.prefix + k
	}
	got, err := p.inner.PeekAll(ctx, prefixed)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(keys))
	for i, k := range keys {
		out[k] = got[prefixed[i]]
	}
	return out, nil
}
