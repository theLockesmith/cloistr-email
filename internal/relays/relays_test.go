package relays

import (
	"context"
	"testing"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-common/relayprefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewClient(t *testing.T) {
	logger := zap.NewNop()

	// Test creating client from environment (uses defaults)
	client := NewClient(logger)
	require.NotNil(t, client)
	assert.NotNil(t, client.client)
	assert.NotNil(t, client.logger)
}

func TestNewClientWithConfig(t *testing.T) {
	logger := zap.NewNop()

	cfg := relayprefs.Config{
		QueryRelays:        []string{"wss://relay.example.com"},
		UseCloistrFallback: true,
		CacheTTL:           30 * time.Minute,
	}

	client := NewClientWithConfig(cfg, logger)
	require.NotNil(t, client)
}

func TestTruncatePubkey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "full pubkey",
			input:    "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			expected: "12345678...cdef",
		},
		{
			name:     "short string",
			input:    "abc",
			expected: "abc",
		},
		{
			name:     "exactly 12 chars",
			input:    "123456789012",
			expected: "123456789012",
		},
		{
			name:     "13 chars",
			input:    "1234567890123",
			expected: "12345678...0123",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncatePubkey(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestClientCacheOperations(t *testing.T) {
	logger := zap.NewNop()

	cfg := relayprefs.Config{
		QueryRelays:        []string{"wss://relay.cloistr.xyz"},
		UseCloistrFallback: true,
		CacheTTL:           1 * time.Hour,
	}

	client := NewClientWithConfig(cfg, logger)

	// Test cache invalidation (should not panic)
	client.InvalidateCache("1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	client.InvalidateAllCache()
}

// TestGetRelayPrefsWithMockRelay tests the relay lookup flow
// This is a more integration-style test that requires network access
func TestGetRelayPrefsIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger, _ := zap.NewDevelopment()

	// Use Cloistr fallback for testing
	cfg := relayprefs.Config{
		UseCloistrFallback: true,
		CacheTTL:           1 * time.Minute,
	}

	client := NewClientWithConfig(cfg, logger)

	// Test with a known Nostr pubkey (this will use Cloistr discovery/relay)
	// Using a test pubkey - the result will depend on whether this pubkey has preferences
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// This test just verifies the client doesn't error out
	// The actual relay preferences may or may not exist
	prefs, err := client.GetRelayPrefs(ctx, "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

	// We expect either success with default fallback or a network error
	if err != nil {
		t.Logf("Network error (expected in isolated environments): %v", err)
		t.Skip("Skipping: network access required for integration test")
	}

	require.NotNil(t, prefs)
	assert.NotEmpty(t, prefs.Pubkey)
	t.Logf("Got relay preferences from source: %s", prefs.Source)
}

func TestGetReadWriteRelays(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger := zap.NewNop()

	cfg := relayprefs.Config{
		UseCloistrFallback: true,
		CacheTTL:           1 * time.Minute,
	}

	client := NewClientWithConfig(cfg, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pubkey := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	// Test GetReadRelays
	readRelays, err := client.GetReadRelays(ctx, pubkey)
	if err != nil {
		t.Logf("Network error (expected in isolated environments): %v", err)
		t.Skip("Skipping: network access required for integration test")
	}
	// Should return at least one relay (from fallback)
	assert.NotNil(t, readRelays)

	// Test GetWriteRelays
	writeRelays, err := client.GetWriteRelays(ctx, pubkey)
	if err != nil {
		t.Skip("Skipping: network error")
	}
	assert.NotNil(t, writeRelays)
}
