package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// StalwartClient manages communication with Stalwart mail server
type StalwartClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	logger     *zap.Logger
}

// Principal represents a Stalwart principal (user/account)
type Principal struct {
	ID                  int64    `json:"id,omitempty"`
	Type                string   `json:"type"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Secrets             []string `json:"secrets,omitempty"`
	Emails              []string `json:"emails,omitempty"`
	Quota               int64    `json:"quota,omitempty"`
	Roles               []string `json:"roles,omitempty"`
	Lists               []string `json:"lists,omitempty"`
	MemberOf            []string `json:"memberOf,omitempty"`
	EnabledPermissions  []string `json:"enabledPermissions,omitempty"`
	DisabledPermissions []string `json:"disabledPermissions,omitempty"`
}

// PrincipalList represents a paginated list of principals
type PrincipalList struct {
	Items []Principal `json:"items"`
	Total int         `json:"total"`
}

// UpdateOperation represents a single update operation for PATCH requests
type UpdateOperation struct {
	Action string      `json:"action"`
	Field  string      `json:"field"`
	Value  interface{} `json:"value,omitempty"`
}

// APIResponse wraps Stalwart API responses
type APIResponse struct {
	Data  json.RawMessage `json:"data,omitempty"`
	Error *APIError       `json:"error,omitempty"`
}

// APIError represents a Stalwart API error
type APIError struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: %s", e.Title, e.Detail)
}

// StalwartError wraps errors from the Stalwart API
type StalwartError struct {
	StatusCode int
	Message    string
	APIError   *APIError
}

func (e *StalwartError) Error() string {
	if e.APIError != nil {
		return e.APIError.Error()
	}
	return fmt.Sprintf("stalwart error (status %d): %s", e.StatusCode, e.Message)
}

// IsNotFound returns true if the error is a 404
func (e *StalwartError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

// IsUnauthorized returns true if the error is a 401
func (e *StalwartError) IsUnauthorized() bool {
	return e.StatusCode == http.StatusUnauthorized
}

// NewStalwartClient creates a new Stalwart client
func NewStalwartClient(baseURL, apiKey string, logger *zap.Logger) (*StalwartClient, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("stalwart base URL is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("stalwart API key is required")
	}

	logger.Info("Initializing Stalwart client", zap.String("base_url", baseURL))

	return &StalwartClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}, nil
}

// doRequest performs an HTTP request with authentication
func (c *StalwartClient) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonData)
	}

	reqURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	c.logger.Debug("Stalwart API request",
		zap.String("method", method),
		zap.String("path", path))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	c.logger.Debug("Stalwart API response",
		zap.Int("status", resp.StatusCode),
		zap.Int("body_length", len(respBody)))

	if resp.StatusCode >= 400 {
		stalwartErr := &StalwartError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
		}

		// Try to parse as API error
		var apiErr APIError
		if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Title != "" {
			stalwartErr.APIError = &apiErr
		}

		return nil, stalwartErr
	}

	return respBody, nil
}

// CreateAccount creates a new email account in Stalwart
func (c *StalwartClient) CreateAccount(ctx context.Context, principal *Principal) (int64, error) {
	c.logger.Debug("Creating Stalwart account", zap.String("name", principal.Name))

	// Ensure type is set
	if principal.Type == "" {
		principal.Type = "individual"
	}

	// Ensure default roles
	if len(principal.Roles) == 0 {
		principal.Roles = []string{"user"}
	}

	respBody, err := c.doRequest(ctx, http.MethodPost, "/api/principal", principal)
	if err != nil {
		return 0, fmt.Errorf("failed to create account: %w", err)
	}

	// Parse response to get the new ID
	var response struct {
		Data int64 `json:"data"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	c.logger.Info("Account created successfully",
		zap.String("name", principal.Name),
		zap.Int64("id", response.Data))

	return response.Data, nil
}

// GetAccount retrieves account information by ID or name
func (c *StalwartClient) GetAccount(ctx context.Context, idOrName string) (*Principal, error) {
	c.logger.Debug("Getting Stalwart account", zap.String("id_or_name", idOrName))

	respBody, err := c.doRequest(ctx, http.MethodGet, "/api/principal/"+url.PathEscape(idOrName), nil)
	if err != nil {
		return nil, err
	}

	var response struct {
		Data Principal `json:"data"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response.Data, nil
}

// UpdateAccount updates an existing account in Stalwart
func (c *StalwartClient) UpdateAccount(ctx context.Context, idOrName string, operations []UpdateOperation) error {
	c.logger.Debug("Updating Stalwart account", zap.String("id_or_name", idOrName))

	_, err := c.doRequest(ctx, http.MethodPatch, "/api/principal/"+url.PathEscape(idOrName), operations)
	if err != nil {
		return fmt.Errorf("failed to update account: %w", err)
	}

	c.logger.Info("Account updated successfully", zap.String("id_or_name", idOrName))
	return nil
}

// DeleteAccount deletes an account from Stalwart
func (c *StalwartClient) DeleteAccount(ctx context.Context, idOrName string) error {
	c.logger.Debug("Deleting Stalwart account", zap.String("id_or_name", idOrName))

	_, err := c.doRequest(ctx, http.MethodDelete, "/api/principal/"+url.PathEscape(idOrName), nil)
	if err != nil {
		return fmt.Errorf("failed to delete account: %w", err)
	}

	c.logger.Info("Account deleted successfully", zap.String("id_or_name", idOrName))
	return nil
}

// ListAccounts lists all accounts with optional pagination and type filter
func (c *StalwartClient) ListAccounts(ctx context.Context, page, limit int, accountType string) (*PrincipalList, error) {
	c.logger.Debug("Listing Stalwart accounts",
		zap.Int("page", page),
		zap.Int("limit", limit),
		zap.String("type", accountType))

	// Build query string
	query := url.Values{}
	if page > 0 {
		query.Set("page", strconv.Itoa(page))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if accountType != "" {
		query.Set("types", accountType)
	}

	path := "/api/principal"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}

	respBody, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	var response struct {
		Data PrincipalList `json:"data"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response.Data, nil
}

// VerifyAccount checks if an account exists in Stalwart
func (c *StalwartClient) VerifyAccount(ctx context.Context, idOrName string) (bool, error) {
	c.logger.Debug("Verifying Stalwart account", zap.String("id_or_name", idOrName))

	_, err := c.GetAccount(ctx, idOrName)
	if err != nil {
		if stalwartErr, ok := err.(*StalwartError); ok && stalwartErr.IsNotFound() {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// SetPassword sets a new password for an account
func (c *StalwartClient) SetPassword(ctx context.Context, idOrName string, password string) error {
	c.logger.Debug("Setting password for account", zap.String("id_or_name", idOrName))

	operations := []UpdateOperation{
		{
			Action: "set",
			Field:  "secrets",
			Value:  []string{password},
		},
	}

	return c.UpdateAccount(ctx, idOrName, operations)
}

// SetQuota sets the storage quota for an account
func (c *StalwartClient) SetQuota(ctx context.Context, idOrName string, quotaBytes int64) error {
	c.logger.Debug("Setting quota for account",
		zap.String("id_or_name", idOrName),
		zap.Int64("quota_bytes", quotaBytes))

	operations := []UpdateOperation{
		{
			Action: "set",
			Field:  "quota",
			Value:  quotaBytes,
		},
	}

	return c.UpdateAccount(ctx, idOrName, operations)
}

// AddEmail adds an email address to an account
func (c *StalwartClient) AddEmail(ctx context.Context, idOrName string, email string) error {
	c.logger.Debug("Adding email to account",
		zap.String("id_or_name", idOrName),
		zap.String("email", email))

	operations := []UpdateOperation{
		{
			Action: "addItem",
			Field:  "emails",
			Value:  email,
		},
	}

	return c.UpdateAccount(ctx, idOrName, operations)
}

// RemoveEmail removes an email address from an account
func (c *StalwartClient) RemoveEmail(ctx context.Context, idOrName string, email string) error {
	c.logger.Debug("Removing email from account",
		zap.String("id_or_name", idOrName),
		zap.String("email", email))

	operations := []UpdateOperation{
		{
			Action: "removeItem",
			Field:  "emails",
			Value:  email,
		},
	}

	return c.UpdateAccount(ctx, idOrName, operations)
}

// Health checks if Stalwart is healthy
func (c *StalwartClient) Health(ctx context.Context) error {
	c.logger.Debug("Checking Stalwart health")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz/live", nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stalwart health check failed: status %d", resp.StatusCode)
	}

	return nil
}

// Ready checks if Stalwart is ready to accept requests
func (c *StalwartClient) Ready(ctx context.Context) error {
	c.logger.Debug("Checking Stalwart readiness")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz/ready", nil)
	if err != nil {
		return fmt.Errorf("failed to create readiness check request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("readiness check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stalwart readiness check failed: status %d", resp.StatusCode)
	}

	return nil
}

// AccountInfo is a convenience struct for creating accounts
type AccountInfo struct {
	Name        string
	Email       string
	Description string
	Password    string
	QuotaBytes  int64
}

// CreateAccountFromInfo creates an account from AccountInfo
func (c *StalwartClient) CreateAccountFromInfo(ctx context.Context, info *AccountInfo) (int64, error) {
	principal := &Principal{
		Type:        "individual",
		Name:        info.Name,
		Description: info.Description,
		Emails:      []string{info.Email},
		Quota:       info.QuotaBytes,
		Roles:       []string{"user"},
	}

	if info.Password != "" {
		principal.Secrets = []string{info.Password}
	}

	return c.CreateAccount(ctx, principal)
}

// GetAccountByEmail finds an account by email address
func (c *StalwartClient) GetAccountByEmail(ctx context.Context, email string) (*Principal, error) {
	c.logger.Debug("Getting account by email", zap.String("email", email))

	// Stalwart allows looking up by email directly
	return c.GetAccount(ctx, email)
}

// EnsureAccount creates an account if it doesn't exist, or returns the existing one
func (c *StalwartClient) EnsureAccount(ctx context.Context, info *AccountInfo) (*Principal, error) {
	c.logger.Debug("Ensuring account exists", zap.String("name", info.Name))

	// Check if account exists
	existing, err := c.GetAccount(ctx, info.Name)
	if err == nil {
		c.logger.Debug("Account already exists", zap.String("name", info.Name))
		return existing, nil
	}

	// If not a 404, return the error
	if stalwartErr, ok := err.(*StalwartError); !ok || !stalwartErr.IsNotFound() {
		return nil, err
	}

	// Create the account
	id, err := c.CreateAccountFromInfo(ctx, info)
	if err != nil {
		return nil, err
	}

	// Fetch and return the created account
	return c.GetAccount(ctx, strconv.FormatInt(id, 10))
}
