// Package transport provides email transport mechanisms.
package transport

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// MXResolver resolves MX records for email domains
type MXResolver struct {
	// cache stores resolved MX records
	cache map[string]*mxCacheEntry
	mu    sync.RWMutex

	// cacheTTL is how long to cache MX records
	cacheTTL time.Duration

	// resolver is the DNS resolver to use (nil = default)
	resolver *net.Resolver
}

type mxCacheEntry struct {
	records   []*net.MX
	expiresAt time.Time
}

// MXResolverOption configures the MX resolver
type MXResolverOption func(*MXResolver)

// WithMXCacheTTL sets the cache TTL for MX records
func WithMXCacheTTL(ttl time.Duration) MXResolverOption {
	return func(r *MXResolver) {
		r.cacheTTL = ttl
	}
}

// WithCustomResolver sets a custom DNS resolver
func WithCustomResolver(resolver *net.Resolver) MXResolverOption {
	return func(r *MXResolver) {
		r.resolver = resolver
	}
}

// NewMXResolver creates a new MX resolver
func NewMXResolver(opts ...MXResolverOption) *MXResolver {
	r := &MXResolver{
		cache:    make(map[string]*mxCacheEntry),
		cacheTTL: 5 * time.Minute, // Default 5 minute cache
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Resolve returns the MX hosts for a domain, sorted by priority (lowest first)
func (r *MXResolver) Resolve(ctx context.Context, domain string) ([]string, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, fmt.Errorf("empty domain")
	}

	// Check cache first
	r.mu.RLock()
	entry, ok := r.cache[domain]
	if ok && time.Now().Before(entry.expiresAt) {
		hosts := mxRecordsToHosts(entry.records)
		r.mu.RUnlock()
		return hosts, nil
	}
	r.mu.RUnlock()

	// Resolve MX records
	var mxRecords []*net.MX
	var err error

	if r.resolver != nil {
		mxRecords, err = r.resolver.LookupMX(ctx, domain)
	} else {
		mxRecords, err = net.LookupMX(domain)
	}

	if err != nil {
		// If no MX records, fall back to A/AAAA record (RFC 5321)
		if dnsErr, ok := err.(*net.DNSError); ok && dnsErr.IsNotFound {
			// Try to resolve the domain directly as a host
			var addrs []string
			if r.resolver != nil {
				addrs, err = r.resolver.LookupHost(ctx, domain)
			} else {
				addrs, err = net.LookupHost(domain)
			}
			if err == nil && len(addrs) > 0 {
				return []string{domain}, nil
			}
		}
		return nil, fmt.Errorf("failed to resolve MX for %s: %w", domain, err)
	}

	if len(mxRecords) == 0 {
		// Fall back to domain itself if no MX records
		return []string{domain}, nil
	}

	// Cache the results
	r.mu.Lock()
	r.cache[domain] = &mxCacheEntry{
		records:   mxRecords,
		expiresAt: time.Now().Add(r.cacheTTL),
	}
	r.mu.Unlock()

	return mxRecordsToHosts(mxRecords), nil
}

// ResolveForEmail extracts the domain from an email address and resolves MX records
func (r *MXResolver) ResolveForEmail(ctx context.Context, email string) ([]string, error) {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid email address: %s", email)
	}

	domain := parts[1]
	return r.Resolve(ctx, domain)
}

// ClearCache clears the MX cache
func (r *MXResolver) ClearCache() {
	r.mu.Lock()
	r.cache = make(map[string]*mxCacheEntry)
	r.mu.Unlock()
}

// mxRecordsToHosts converts MX records to a sorted list of hostnames
func mxRecordsToHosts(records []*net.MX) []string {
	// Sort by preference (lower is better)
	sorted := make([]*net.MX, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Pref < sorted[j].Pref
	})

	hosts := make([]string, len(sorted))
	for i, mx := range sorted {
		// Remove trailing dot from hostname
		host := strings.TrimSuffix(mx.Host, ".")
		hosts[i] = host
	}

	return hosts
}

// DeliveryMode represents the email delivery mode
type DeliveryMode string

const (
	// DeliveryModeRelay sends all email through a configured relay server
	DeliveryModeRelay DeliveryMode = "relay"

	// DeliveryModeDirect delivers email directly to recipient MX servers
	DeliveryModeDirect DeliveryMode = "direct"

	// DeliveryModeHybrid uses direct delivery for known domains, relay for others
	DeliveryModeHybrid DeliveryMode = "hybrid"
)

// DirectDeliveryConfig contains configuration for direct MX-based delivery
type DirectDeliveryConfig struct {
	// Mode specifies the delivery mode
	Mode DeliveryMode

	// RelayConfig is used for relay mode or hybrid fallback
	RelayConfig *SMTPConfig

	// LocalDomains are domains we deliver directly (for hybrid mode)
	// Mail to these domains bypasses the relay
	LocalDomains []string

	// DKIM configuration for signing outbound mail
	DKIM *DKIMConfig

	// Timeout for direct delivery connections
	Timeout time.Duration

	// RetryCount is the number of MX servers to try before failing
	RetryCount int

	// HELO hostname to use for direct delivery
	LocalName string
}

// DefaultDirectDeliveryConfig returns sensible defaults for direct delivery
func DefaultDirectDeliveryConfig() *DirectDeliveryConfig {
	return &DirectDeliveryConfig{
		Mode:       DeliveryModeRelay,
		Timeout:    30 * time.Second,
		RetryCount: 3,
		LocalName:  "localhost",
	}
}
