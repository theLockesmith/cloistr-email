// Package identity provides address verification via cloistr-me.
//
// Usage:
//
// To enable address verification via cloistr-me's internal API, set these
// environment variables:
//
//	CLOISTR_ME_URL=http://cloistr-me.cloistr.svc.cluster.local:8080
//	CLOISTR_ME_SECRET=<shared-secret-with-cloistr-me>
//
// The secret must match the INTERNAL_API_SECRET configured in cloistr-me.
//
// Then wire up the client when creating the identity service:
//
//	// Create the cloistr-me client
//	cloistrMeClient := identity.NewCloistrMeClient(
//		cfg.CloistrMeURL,
//		cfg.CloistrMeSecret,
//		logger,
//	)
//
//	// Create identity service with verifier
//	identitySvc := identity.NewService(addressStore, nip05Resolver, logger).
//		WithVerifier(cloistrMeClient)
//
//	// Pass to email service
//	emailSvc := email.NewService(identitySvc, transportMgr, encryptionSvc, db, logger)
//
// When CLOISTR_ME_URL or CLOISTR_ME_SECRET are not configured, verification
// is skipped for backwards compatibility.
package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"go.uber.org/zap"
)

// CloistrMeClient handles address verification calls to cloistr-me internal API.
// It verifies that a pubkey owns a specific @cloistr.xyz address before allowing
// email operations.
type CloistrMeClient struct {
	baseURL    string
	secret     string
	httpClient *http.Client
	logger     *zap.Logger
}

// VerifyAddressResponse is the response from the address verification endpoint.
type VerifyAddressResponse struct {
	Owned   bool   `json:"owned"`
	Address string `json:"address"` // Full email address (e.g., "alice@cloistr.xyz")
	Pubkey  string `json:"pubkey"`  // Hex pubkey
	Error   string `json:"error,omitempty"`
}

// NewCloistrMeClient creates a new client for cloistr-me internal API.
func NewCloistrMeClient(baseURL, secret string, logger *zap.Logger) *CloistrMeClient {
	return &CloistrMeClient{
		baseURL: baseURL,
		secret:  secret,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// VerifyAddressOwnership checks if a pubkey owns a specific address.
// This is called before allowing email send operations to ensure the sender
// has authority over the from address.
func (c *CloistrMeClient) VerifyAddressOwnership(ctx context.Context, pubkey, address string) (bool, error) {
	if c.baseURL == "" {
		c.logger.Warn("cloistr-me URL not configured, skipping address verification")
		return true, nil // Allow if not configured (for backwards compatibility)
	}

	if c.secret == "" {
		c.logger.Warn("cloistr-me secret not configured, skipping address verification")
		return true, nil
	}

	// Build request URL
	endpoint := fmt.Sprintf("%s/internal/v1/addresses/verify", c.baseURL)
	reqURL, err := url.Parse(endpoint)
	if err != nil {
		return false, fmt.Errorf("invalid cloistr-me URL: %w", err)
	}

	// Add query parameters
	q := reqURL.Query()
	q.Set("pubkey", pubkey)
	q.Set("address", address)
	reqURL.RawQuery = q.Encode()

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authorization header
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Content-Type", "application/json")

	c.logger.Debug("verifying address ownership",
		zap.String("pubkey", truncateKey(pubkey)),
		zap.String("address", address))

	// Make request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("cloistr-me request failed",
			zap.Error(err),
			zap.String("url", reqURL.String()))
		return false, fmt.Errorf("cloistr-me request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle response
	if resp.StatusCode == http.StatusUnauthorized {
		return false, fmt.Errorf("cloistr-me authentication failed: invalid secret")
	}

	if resp.StatusCode == http.StatusNotFound {
		// Address not found in cloistr-me - not owned
		c.logger.Debug("address not found in cloistr-me",
			zap.String("address", address))
		return false, nil
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("cloistr-me returned status %d", resp.StatusCode)
	}

	// Parse response
	var verifyResp VerifyAddressResponse
	if err := json.NewDecoder(resp.Body).Decode(&verifyResp); err != nil {
		return false, fmt.Errorf("failed to parse cloistr-me response: %w", err)
	}

	if verifyResp.Error != "" {
		return false, fmt.Errorf("cloistr-me error: %s", verifyResp.Error)
	}

	c.logger.Debug("address verification result",
		zap.String("address", address),
		zap.Bool("owned", verifyResp.Owned))

	return verifyResp.Owned, nil
}

// truncateKey truncates a hex key for logging
func truncateKey(key string) string {
	if len(key) > 16 {
		return key[:16] + "..."
	}
	return key
}
