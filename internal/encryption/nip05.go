package encryption

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coldforge/coldforge-email/internal/metrics"
	"go.uber.org/zap"
)

// Configuration constants
const (
	nip05HTTPTimeout     = 5 * time.Second
	nip05DefaultCacheTTL = 24 * time.Hour
	nip05MaxResponseSize = 1 << 20 // 1MB limit on response body
)

// NIP05Response represents the JSON response from .well-known/nostr.json
type NIP05Response struct {
	Names  map[string]string   `json:"names"`
	Relays map[string][]string `json:"relays,omitempty"`
}

// NIP05Resolver resolves email addresses to Nostr pubkeys using NIP-05
type NIP05Resolver struct {
	client   *http.Client
	cache    map[string]*cacheEntry
	cacheMu  sync.RWMutex
	cacheTTL time.Duration
	logger   *zap.Logger
}

type cacheEntry struct {
	pubkey    string
	expiresAt time.Time
}

// NewNIP05Resolver creates a new NIP-05 resolver
func NewNIP05Resolver(logger *zap.Logger) *NIP05Resolver {
	return &NIP05Resolver{
		client: &http.Client{
			Timeout: nip05HTTPTimeout,
		},
		cache:    make(map[string]*cacheEntry),
		cacheTTL: nip05DefaultCacheTTL,
		logger:   logger,
	}
}

// ResolvePubkey looks up the npub for an email address using NIP-05
// The email format should be user@domain.com
// This queries https://domain.com/.well-known/nostr.json?name=user
func (r *NIP05Resolver) ResolvePubkey(ctx context.Context, email string) (string, error) {
	// Parse email address
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid email format: %s", email)
	}

	name := parts[0]
	domain := parts[1]

	// Normalize: use "_" for root domain identifiers
	if name == "" {
		name = "_"
	}

	cacheKey := fmt.Sprintf("%s@%s", name, domain)

	// Check cache first
	r.cacheMu.RLock()
	if entry, ok := r.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		r.cacheMu.RUnlock()
		r.logger.Debug("NIP-05 cache hit", zap.String("email", email))
		metrics.NIP05LookupsTotal.WithLabelValues("cached").Inc()
		return entry.pubkey, nil
	}
	r.cacheMu.RUnlock()

	// Start timing for non-cached lookup
	lookupStart := time.Now()

	// Query the .well-known/nostr.json endpoint
	url := fmt.Sprintf("https://%s/.well-known/nostr.json?name=%s", domain, name)

	r.logger.Debug("Querying NIP-05", zap.String("url", url))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		metrics.NIP05LookupsTotal.WithLabelValues("failure").Inc()
		metrics.NIP05LookupDuration.Observe(time.Since(lookupStart).Seconds())
		return "", fmt.Errorf("NIP-05 lookup failed for %s: %w", email, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		metrics.NIP05LookupsTotal.WithLabelValues("failure").Inc()
		metrics.NIP05LookupDuration.Observe(time.Since(lookupStart).Seconds())
		return "", fmt.Errorf("no NIP-05 record found for %s", email)
	}

	if resp.StatusCode != http.StatusOK {
		metrics.NIP05LookupsTotal.WithLabelValues("failure").Inc()
		metrics.NIP05LookupDuration.Observe(time.Since(lookupStart).Seconds())
		return "", fmt.Errorf("NIP-05 lookup returned status %d for %s", resp.StatusCode, email)
	}

	// Limit response body size to prevent memory exhaustion attacks
	limitedBody := io.LimitReader(resp.Body, nip05MaxResponseSize)

	var nip05Resp NIP05Response
	if err := json.NewDecoder(limitedBody).Decode(&nip05Resp); err != nil {
		return "", fmt.Errorf("failed to parse NIP-05 response: %w", err)
	}

	// Look up the pubkey for the name (case-insensitive)
	var pubkey string
	for n, p := range nip05Resp.Names {
		if strings.EqualFold(n, name) {
			pubkey = p
			break
		}
	}

	if pubkey == "" {
		metrics.NIP05LookupsTotal.WithLabelValues("failure").Inc()
		metrics.NIP05LookupDuration.Observe(time.Since(lookupStart).Seconds())
		return "", fmt.Errorf("no pubkey found for %s in NIP-05 response", email)
	}

	// Validate pubkey format (must be 64 hex characters)
	if len(pubkey) != 64 {
		metrics.NIP05LookupsTotal.WithLabelValues("failure").Inc()
		metrics.NIP05LookupDuration.Observe(time.Since(lookupStart).Seconds())
		return "", fmt.Errorf("invalid pubkey length in NIP-05 response: expected 64, got %d", len(pubkey))
	}
	if _, err := hex.DecodeString(pubkey); err != nil {
		metrics.NIP05LookupsTotal.WithLabelValues("failure").Inc()
		metrics.NIP05LookupDuration.Observe(time.Since(lookupStart).Seconds())
		return "", fmt.Errorf("invalid pubkey format in NIP-05 response: not valid hex")
	}

	// Record successful lookup
	metrics.NIP05LookupsTotal.WithLabelValues("success").Inc()
	metrics.NIP05LookupDuration.Observe(time.Since(lookupStart).Seconds())

	// Cache the result
	r.cacheMu.Lock()
	r.cache[cacheKey] = &cacheEntry{
		pubkey:    pubkey,
		expiresAt: time.Now().Add(r.cacheTTL),
	}
	metrics.NIP05CacheSize.Set(float64(len(r.cache)))
	r.cacheMu.Unlock()

	r.logger.Info("NIP-05 resolved",
		zap.String("email", email),
		zap.String("pubkey", truncatePubkey(pubkey)))

	return pubkey, nil
}

// ClearCache clears the NIP-05 cache
func (r *NIP05Resolver) ClearCache() {
	r.cacheMu.Lock()
	r.cache = make(map[string]*cacheEntry)
	metrics.NIP05CacheSize.Set(0)
	r.cacheMu.Unlock()
}

// CacheStats returns the number of cached entries
func (r *NIP05Resolver) CacheStats() int {
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()
	return len(r.cache)
}

// SetCacheTTL updates the cache TTL for future entries
func (r *NIP05Resolver) SetCacheTTL(ttl time.Duration) {
	r.cacheMu.Lock()
	r.cacheTTL = ttl
	r.cacheMu.Unlock()
}

// CompositeKeyResolver tries multiple resolution methods
type CompositeKeyResolver struct {
	resolvers []KeyResolver
	logger    *zap.Logger
}

// NewCompositeKeyResolver creates a resolver that tries multiple sources
func NewCompositeKeyResolver(logger *zap.Logger, resolvers ...KeyResolver) *CompositeKeyResolver {
	return &CompositeKeyResolver{
		resolvers: resolvers,
		logger:    logger,
	}
}

// ResolvePubkey tries each resolver in order until one succeeds
func (c *CompositeKeyResolver) ResolvePubkey(ctx context.Context, email string) (string, error) {
	var lastErr error

	for _, resolver := range c.resolvers {
		pubkey, err := resolver.ResolvePubkey(ctx, email)
		if err == nil {
			return pubkey, nil
		}
		lastErr = err
		c.logger.Debug("Resolver failed, trying next",
			zap.String("email", email),
			zap.Error(err))
	}

	if lastErr != nil {
		return "", fmt.Errorf("all resolvers failed: %w", lastErr)
	}

	return "", fmt.Errorf("no resolvers available")
}

// AddResolver adds a resolver to the chain
func (c *CompositeKeyResolver) AddResolver(resolver KeyResolver) {
	c.resolvers = append(c.resolvers, resolver)
}
