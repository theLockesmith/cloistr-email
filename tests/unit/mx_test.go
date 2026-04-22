package unit

import (
	"context"
	"testing"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMXResolver(t *testing.T) {
	t.Run("creates resolver with default options", func(t *testing.T) {
		resolver := transport.NewMXResolver()
		require.NotNil(t, resolver)
	})

	t.Run("creates resolver with custom TTL", func(t *testing.T) {
		resolver := transport.NewMXResolver(transport.WithMXCacheTTL(10 * time.Minute))
		require.NotNil(t, resolver)
	})
}

func TestMXResolverResolve(t *testing.T) {
	resolver := transport.NewMXResolver()

	t.Run("empty domain returns error", func(t *testing.T) {
		hosts, err := resolver.Resolve(context.Background(), "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "empty domain")
		assert.Nil(t, hosts)
	})

	t.Run("whitespace domain returns error", func(t *testing.T) {
		hosts, err := resolver.Resolve(context.Background(), "   ")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "empty domain")
		assert.Nil(t, hosts)
	})

	// Note: We can't easily test actual DNS lookups in unit tests without mocking
	// Real DNS resolution is tested in integration tests
}

func TestMXResolverResolveForEmail(t *testing.T) {
	resolver := transport.NewMXResolver()

	t.Run("invalid email without @ returns error", func(t *testing.T) {
		hosts, err := resolver.ResolveForEmail(context.Background(), "notanemail")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid email address")
		assert.Nil(t, hosts)
	})

	t.Run("email with multiple @ returns error", func(t *testing.T) {
		hosts, err := resolver.ResolveForEmail(context.Background(), "user@domain@example.com")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid email address")
		assert.Nil(t, hosts)
	})

	t.Run("empty email returns error", func(t *testing.T) {
		hosts, err := resolver.ResolveForEmail(context.Background(), "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid email address")
		assert.Nil(t, hosts)
	})
}

func TestMXResolverClearCache(t *testing.T) {
	resolver := transport.NewMXResolver()

	// ClearCache should not panic on empty cache
	resolver.ClearCache()

	// After clearing, we should be able to continue using the resolver
	// This is a basic sanity check
	_, _ = resolver.Resolve(context.Background(), "nonexistent.example.com")
}

func TestDeliveryModeConstants(t *testing.T) {
	t.Run("delivery mode values", func(t *testing.T) {
		assert.Equal(t, transport.DeliveryMode("relay"), transport.DeliveryModeRelay)
		assert.Equal(t, transport.DeliveryMode("direct"), transport.DeliveryModeDirect)
		assert.Equal(t, transport.DeliveryMode("hybrid"), transport.DeliveryModeHybrid)
	})
}

func TestDefaultDirectDeliveryConfig(t *testing.T) {
	config := transport.DefaultDirectDeliveryConfig()

	assert.Equal(t, transport.DeliveryModeRelay, config.Mode)
	assert.Equal(t, 30*time.Second, config.Timeout)
	assert.Equal(t, 3, config.RetryCount)
	assert.Equal(t, "localhost", config.LocalName)
}
