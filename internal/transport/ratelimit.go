// Package transport provides email transport mechanisms.
package transport

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RateLimiter provides rate limiting for SMTP connections and messages
type RateLimiter struct {
	mu sync.RWMutex

	// Per-IP connection tracking
	connections map[string]*rateLimitEntry

	// Per-IP message tracking
	messages map[string]*rateLimitEntry

	// Configuration
	config *RateLimitConfig

	// Cleanup ticker
	cleanupTicker *time.Ticker
	done          chan struct{}
}

// rateLimitEntry tracks rate limit state for a single IP
type rateLimitEntry struct {
	count     int
	window    time.Time
	blocked   bool
	blockedAt time.Time
}

// RateLimitConfig contains rate limiting configuration
type RateLimitConfig struct {
	// ConnectionsPerMinute is the max connections per IP per minute
	ConnectionsPerMinute int

	// MessagesPerMinute is the max messages per IP per minute
	MessagesPerMinute int

	// MessagesPerHour is the max messages per IP per hour
	MessagesPerHour int

	// RecipientsPerMessage is the max recipients per message
	RecipientsPerMessage int

	// BlockDuration is how long to block an IP after exceeding limits
	BlockDuration time.Duration

	// WhitelistedIPs are IPs exempt from rate limiting
	WhitelistedIPs []string

	// CleanupInterval is how often to clean up old entries
	CleanupInterval time.Duration
}

// DefaultRateLimitConfig returns sensible defaults
func DefaultRateLimitConfig() *RateLimitConfig {
	return &RateLimitConfig{
		ConnectionsPerMinute: 30,
		MessagesPerMinute:    10,
		MessagesPerHour:      100,
		RecipientsPerMessage: 50,
		BlockDuration:        15 * time.Minute,
		CleanupInterval:      5 * time.Minute,
	}
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(config *RateLimitConfig) *RateLimiter {
	if config == nil {
		config = DefaultRateLimitConfig()
	}

	rl := &RateLimiter{
		connections: make(map[string]*rateLimitEntry),
		messages:    make(map[string]*rateLimitEntry),
		config:      config,
		done:        make(chan struct{}),
	}

	// Start cleanup goroutine
	rl.cleanupTicker = time.NewTicker(config.CleanupInterval)
	go rl.cleanupLoop()

	return rl
}

// AllowConnection checks if a connection from the given IP is allowed
func (rl *RateLimiter) AllowConnection(ip string) error {
	if rl.isWhitelisted(ip) {
		return nil
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry := rl.getOrCreateEntry(rl.connections, ip)

	// Check if blocked
	if entry.blocked {
		if time.Since(entry.blockedAt) < rl.config.BlockDuration {
			return &RateLimitError{
				IP:      ip,
				Reason:  "temporarily blocked",
				RetryAt: entry.blockedAt.Add(rl.config.BlockDuration),
			}
		}
		// Block expired, reset
		entry.blocked = false
		entry.count = 0
		entry.window = time.Now()
	}

	// Check window
	if time.Since(entry.window) > time.Minute {
		entry.count = 0
		entry.window = time.Now()
	}

	entry.count++

	if entry.count > rl.config.ConnectionsPerMinute {
		entry.blocked = true
		entry.blockedAt = time.Now()
		return &RateLimitError{
			IP:      ip,
			Reason:  "too many connections",
			RetryAt: entry.blockedAt.Add(rl.config.BlockDuration),
		}
	}

	return nil
}

// AllowMessage checks if a message from the given IP is allowed
func (rl *RateLimiter) AllowMessage(ip string, recipientCount int) error {
	if rl.isWhitelisted(ip) {
		return nil
	}

	// Check recipient count
	if recipientCount > rl.config.RecipientsPerMessage {
		return &RateLimitError{
			IP:     ip,
			Reason: fmt.Sprintf("too many recipients (%d > %d)", recipientCount, rl.config.RecipientsPerMessage),
		}
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry := rl.getOrCreateEntry(rl.messages, ip)

	// Check if blocked
	if entry.blocked {
		if time.Since(entry.blockedAt) < rl.config.BlockDuration {
			return &RateLimitError{
				IP:      ip,
				Reason:  "temporarily blocked",
				RetryAt: entry.blockedAt.Add(rl.config.BlockDuration),
			}
		}
		// Block expired, reset
		entry.blocked = false
		entry.count = 0
		entry.window = time.Now()
	}

	// Check window
	if time.Since(entry.window) > time.Minute {
		entry.count = 0
		entry.window = time.Now()
	}

	entry.count++

	if entry.count > rl.config.MessagesPerMinute {
		entry.blocked = true
		entry.blockedAt = time.Now()
		return &RateLimitError{
			IP:      ip,
			Reason:  "too many messages per minute",
			RetryAt: entry.blockedAt.Add(rl.config.BlockDuration),
		}
	}

	return nil
}

// RecordMessage records a successful message delivery (for hourly tracking)
func (rl *RateLimiter) RecordMessage(ip string) {
	// This could be extended to track hourly limits using a separate map
	// For now, the per-minute limit is sufficient
}

// getOrCreateEntry gets or creates a rate limit entry for an IP
func (rl *RateLimiter) getOrCreateEntry(m map[string]*rateLimitEntry, ip string) *rateLimitEntry {
	entry, ok := m[ip]
	if !ok {
		entry = &rateLimitEntry{
			window: time.Now(),
		}
		m[ip] = entry
	}
	return entry
}

// isWhitelisted checks if an IP is whitelisted
func (rl *RateLimiter) isWhitelisted(ip string) bool {
	for _, whitelisted := range rl.config.WhitelistedIPs {
		if ip == whitelisted {
			return true
		}
	}
	return false
}

// cleanupLoop periodically cleans up old entries
func (rl *RateLimiter) cleanupLoop() {
	for {
		select {
		case <-rl.cleanupTicker.C:
			rl.cleanup()
		case <-rl.done:
			return
		}
	}
}

// cleanup removes old entries
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-rl.config.BlockDuration * 2)

	for ip, entry := range rl.connections {
		if entry.window.Before(cutoff) && !entry.blocked {
			delete(rl.connections, ip)
		}
	}

	for ip, entry := range rl.messages {
		if entry.window.Before(cutoff) && !entry.blocked {
			delete(rl.messages, ip)
		}
	}
}

// Stop stops the rate limiter cleanup goroutine
func (rl *RateLimiter) Stop() {
	rl.cleanupTicker.Stop()
	close(rl.done)
}

// Stats returns current rate limiter statistics
func (rl *RateLimiter) Stats() RateLimitStats {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	stats := RateLimitStats{
		TrackedIPs:    len(rl.connections),
		BlockedIPs:    0,
		TotalMessages: 0,
	}

	for _, entry := range rl.connections {
		if entry.blocked {
			stats.BlockedIPs++
		}
	}

	for _, entry := range rl.messages {
		stats.TotalMessages += entry.count
	}

	return stats
}

// RateLimitStats contains rate limiter statistics
type RateLimitStats struct {
	TrackedIPs    int
	BlockedIPs    int
	TotalMessages int
}

// RateLimitError is returned when rate limits are exceeded
type RateLimitError struct {
	IP      string
	Reason  string
	RetryAt time.Time
}

func (e *RateLimitError) Error() string {
	if e.RetryAt.IsZero() {
		return fmt.Sprintf("rate limit exceeded for %s: %s", e.IP, e.Reason)
	}
	return fmt.Sprintf("rate limit exceeded for %s: %s (retry after %s)", e.IP, e.Reason, e.RetryAt.Format(time.RFC3339))
}

// RateLimitMiddleware wraps a MessageHandler with rate limiting
type RateLimitMiddleware struct {
	handler     MessageHandler
	rateLimiter *RateLimiter
}

// NewRateLimitMiddleware creates a new rate limiting middleware
func NewRateLimitMiddleware(handler MessageHandler, rateLimiter *RateLimiter) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		handler:     handler,
		rateLimiter: rateLimiter,
	}
}

// HandleMessage implements MessageHandler with rate limiting
func (m *RateLimitMiddleware) HandleMessage(ctx context.Context, from string, to []string, data []byte) error {
	// Extract IP from context if available
	ip := extractIPFromContext(ctx)
	if ip == "" {
		ip = "unknown"
	}

	// Check message rate limit
	if err := m.rateLimiter.AllowMessage(ip, len(to)); err != nil {
		return NewPermanentError(err)
	}

	// Call underlying handler
	err := m.handler.HandleMessage(ctx, from, to, data)

	// Record successful message
	if err == nil {
		m.rateLimiter.RecordMessage(ip)
	}

	return err
}

// Context key for storing client IP
type contextKey string

const clientIPKey contextKey = "client_ip"

// WithClientIP adds the client IP to the context
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey, ip)
}

// extractIPFromContext extracts the client IP from context
func extractIPFromContext(ctx context.Context) string {
	if ip, ok := ctx.Value(clientIPKey).(string); ok {
		return ip
	}
	return ""
}
