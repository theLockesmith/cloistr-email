// Package relays provides relay preference discovery for cloistr-email.
// It wraps cloistr-common/relayprefs to provide a consistent interface
// for discovering user relay preferences across the email service.
package relays

import (
	"context"

	"git.aegis-hq.xyz/coldforge/cloistr-common/relayprefs"
	"go.uber.org/zap"
)

// Client wraps the cloistr-common relayprefs client with logging.
type Client struct {
	client *relayprefs.Client
	logger *zap.Logger
}

// NewClient creates a new relay preferences client from environment variables.
// See cloistr-common/relayprefs for supported environment variables:
//   - DISCOVERY_INTERNAL: URL of self-hosted discovery service
//   - RELAY_LIST: Comma-separated list of relay URLs
//   - DISCOVERY_EXTERNAL: URL of third-party discovery service
//   - USE_CLOISTR_FALLBACK: "true" (default) or "false"
//   - RELAY_PREFS_CACHE_TTL: Cache duration (e.g., "1h")
func NewClient(logger *zap.Logger) *Client {
	client := relayprefs.NewClientFromEnv()

	if err := client.Validate(); err != nil {
		logger.Warn("Relay preferences client validation warning",
			zap.Error(err),
			zap.String("hint", "using Cloistr fallback"))
	}

	cfg := client.Config()
	logger.Info("Relay preferences client initialized",
		zap.String("internal_discovery", cfg.InternalDiscovery),
		zap.Int("query_relays", len(cfg.QueryRelays)),
		zap.String("external_discovery", cfg.ExternalDiscovery),
		zap.Bool("cloistr_fallback", cfg.UseCloistrFallback),
		zap.Duration("cache_ttl", cfg.CacheTTL))

	return &Client{
		client: client,
		logger: logger,
	}
}

// NewClientWithConfig creates a relay preferences client with explicit configuration.
func NewClientWithConfig(cfg relayprefs.Config, logger *zap.Logger) *Client {
	return &Client{
		client: relayprefs.NewClient(cfg),
		logger: logger,
	}
}

// GetRelayPrefs retrieves relay preferences for a pubkey.
// Returns the user's preferred relays for reading and writing.
func (c *Client) GetRelayPrefs(ctx context.Context, pubkey string) (*relayprefs.RelayPrefs, error) {
	c.logger.Debug("Looking up relay preferences", zap.String("pubkey", truncatePubkey(pubkey)))

	prefs, err := c.client.GetRelayPrefs(ctx, pubkey)
	if err != nil {
		c.logger.Error("Failed to get relay preferences",
			zap.String("pubkey", truncatePubkey(pubkey)),
			zap.Error(err))
		return nil, err
	}

	c.logger.Debug("Got relay preferences",
		zap.String("pubkey", truncatePubkey(pubkey)),
		zap.String("source", prefs.Source),
		zap.Int("relay_count", len(prefs.Relays)))

	return prefs, nil
}

// GetReadRelays returns the read relay URLs for a pubkey.
func (c *Client) GetReadRelays(ctx context.Context, pubkey string) ([]string, error) {
	prefs, err := c.GetRelayPrefs(ctx, pubkey)
	if err != nil {
		return nil, err
	}
	return prefs.ReadRelays(), nil
}

// GetWriteRelays returns the write relay URLs for a pubkey.
func (c *Client) GetWriteRelays(ctx context.Context, pubkey string) ([]string, error) {
	prefs, err := c.GetRelayPrefs(ctx, pubkey)
	if err != nil {
		return nil, err
	}
	return prefs.WriteRelays(), nil
}

// InvalidateCache removes cached preferences for a pubkey.
func (c *Client) InvalidateCache(pubkey string) {
	c.client.InvalidateCache(pubkey)
	c.logger.Debug("Invalidated relay prefs cache", zap.String("pubkey", truncatePubkey(pubkey)))
}

// InvalidateAllCache clears the entire cache.
func (c *Client) InvalidateAllCache() {
	c.client.InvalidateAllCache()
	c.logger.Debug("Invalidated all relay prefs cache")
}

// truncatePubkey truncates a pubkey for logging (first 8 + last 4 chars).
func truncatePubkey(pubkey string) string {
	if len(pubkey) <= 12 {
		return pubkey
	}
	return pubkey[:8] + "..." + pubkey[len(pubkey)-4:]
}
