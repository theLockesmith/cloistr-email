package unit

import (
	"testing"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultRateLimitConfig(t *testing.T) {
	config := transport.DefaultRateLimitConfig()

	assert.Equal(t, 30, config.ConnectionsPerMinute)
	assert.Equal(t, 10, config.MessagesPerMinute)
	assert.Equal(t, 100, config.MessagesPerHour)
	assert.Equal(t, 50, config.RecipientsPerMessage)
	assert.Equal(t, 15*time.Minute, config.BlockDuration)
	assert.Equal(t, 5*time.Minute, config.CleanupInterval)
}

func TestNewRateLimiter(t *testing.T) {
	t.Run("with default config", func(t *testing.T) {
		rl := transport.NewRateLimiter(nil)
		require.NotNil(t, rl)
		defer rl.Stop()
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &transport.RateLimitConfig{
			ConnectionsPerMinute: 10,
			MessagesPerMinute:    5,
			BlockDuration:        5 * time.Minute,
			CleanupInterval:      1 * time.Minute,
		}
		rl := transport.NewRateLimiter(config)
		require.NotNil(t, rl)
		defer rl.Stop()
	})
}

func TestRateLimiterAllowConnection(t *testing.T) {
	config := &transport.RateLimitConfig{
		ConnectionsPerMinute: 3,
		MessagesPerMinute:    10,
		BlockDuration:        1 * time.Second,
		CleanupInterval:      1 * time.Hour, // Don't cleanup during test
	}
	rl := transport.NewRateLimiter(config)
	defer rl.Stop()

	ip := "192.168.1.100"

	t.Run("allows connections under limit", func(t *testing.T) {
		err := rl.AllowConnection(ip)
		assert.NoError(t, err)

		err = rl.AllowConnection(ip)
		assert.NoError(t, err)

		err = rl.AllowConnection(ip)
		assert.NoError(t, err)
	})

	t.Run("blocks connections over limit", func(t *testing.T) {
		// This should exceed the limit
		err := rl.AllowConnection(ip)
		assert.Error(t, err)

		var rlErr *transport.RateLimitError
		assert.ErrorAs(t, err, &rlErr)
		assert.Contains(t, rlErr.Reason, "too many connections")
	})

	t.Run("unblocks after block duration", func(t *testing.T) {
		// Wait for block to expire
		time.Sleep(1100 * time.Millisecond)

		err := rl.AllowConnection(ip)
		assert.NoError(t, err)
	})
}

func TestRateLimiterAllowMessage(t *testing.T) {
	config := &transport.RateLimitConfig{
		ConnectionsPerMinute: 100,
		MessagesPerMinute:    2,
		RecipientsPerMessage: 5,
		BlockDuration:        1 * time.Second,
		CleanupInterval:      1 * time.Hour,
	}
	rl := transport.NewRateLimiter(config)
	defer rl.Stop()

	ip := "192.168.1.101"

	t.Run("allows messages under limit", func(t *testing.T) {
		err := rl.AllowMessage(ip, 1)
		assert.NoError(t, err)

		err = rl.AllowMessage(ip, 2)
		assert.NoError(t, err)
	})

	t.Run("blocks messages over limit", func(t *testing.T) {
		err := rl.AllowMessage(ip, 1)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too many messages")
	})

	t.Run("rejects too many recipients", func(t *testing.T) {
		newIP := "192.168.1.102"
		err := rl.AllowMessage(newIP, 10) // Over the 5 recipient limit
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too many recipients")
	})
}

func TestRateLimiterWhitelist(t *testing.T) {
	config := &transport.RateLimitConfig{
		ConnectionsPerMinute: 1,
		MessagesPerMinute:    1,
		WhitelistedIPs:       []string{"10.0.0.1", "127.0.0.1"},
		CleanupInterval:      1 * time.Hour,
	}
	rl := transport.NewRateLimiter(config)
	defer rl.Stop()

	t.Run("whitelisted IP bypasses limits", func(t *testing.T) {
		// Should allow many connections from whitelisted IP
		for i := 0; i < 100; i++ {
			err := rl.AllowConnection("10.0.0.1")
			assert.NoError(t, err)
		}

		for i := 0; i < 100; i++ {
			err := rl.AllowMessage("127.0.0.1", 10)
			assert.NoError(t, err)
		}
	})

	t.Run("non-whitelisted IP is limited", func(t *testing.T) {
		ip := "192.168.1.200"

		err := rl.AllowConnection(ip)
		assert.NoError(t, err)

		// Second connection should be blocked
		err = rl.AllowConnection(ip)
		assert.Error(t, err)
	})
}

func TestRateLimiterStats(t *testing.T) {
	config := &transport.RateLimitConfig{
		ConnectionsPerMinute: 100,
		MessagesPerMinute:    100,
		CleanupInterval:      1 * time.Hour,
	}
	rl := transport.NewRateLimiter(config)
	defer rl.Stop()

	// Make some connections
	rl.AllowConnection("192.168.1.1")
	rl.AllowConnection("192.168.1.2")
	rl.AllowMessage("192.168.1.1", 1)

	stats := rl.Stats()
	assert.GreaterOrEqual(t, stats.TrackedIPs, 2)
	assert.Equal(t, 0, stats.BlockedIPs)
}

func TestRateLimitError(t *testing.T) {
	t.Run("error without retry time", func(t *testing.T) {
		err := &transport.RateLimitError{
			IP:     "192.168.1.1",
			Reason: "test reason",
		}
		assert.Contains(t, err.Error(), "192.168.1.1")
		assert.Contains(t, err.Error(), "test reason")
	})

	t.Run("error with retry time", func(t *testing.T) {
		retryAt := time.Now().Add(5 * time.Minute)
		err := &transport.RateLimitError{
			IP:      "192.168.1.1",
			Reason:  "test reason",
			RetryAt: retryAt,
		}
		assert.Contains(t, err.Error(), "192.168.1.1")
		assert.Contains(t, err.Error(), "retry after")
	})
}
