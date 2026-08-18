package blossom

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ErrNoServers is returned when an operation is attempted with an empty
// server list.
var ErrNoServers = errors.New("blossom: no servers configured")

// defaultAuthTTL is how long a signed authorization event remains valid.
const defaultAuthTTL = 5 * time.Minute

// maxBlobSize bounds how many bytes we will read from a Blossom server
// response, guarding against a malicious server streaming an unbounded body.
// Email bodies and attachments are well under this.
const maxBlobSize = 50 << 20 // 50 MiB

// ErrBlobTooLarge is returned when a server's response exceeds maxBlobSize.
var ErrBlobTooLarge = fmt.Errorf("blossom: blob exceeds %d bytes", maxBlobSize)

// hashPattern matches a 64-char lowercase hex sha256 address.
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Server is a single Blossom endpoint with a selection priority (lower is
// tried first).
type Server struct {
	URL      string `json:"url"`
	Priority int    `json:"priority"`
}

// BlobDescriptor describes a stored blob and where it landed.
type BlobDescriptor struct {
	SHA256  string   `json:"sha256"`
	Size    int64    `json:"size"`
	Type    string   `json:"type,omitempty"`
	Servers []string `json:"servers"` // base URLs the blob was successfully stored to
}

// Client talks to Blossom servers (BUD-01/02). It is safe for concurrent use.
type Client struct {
	http    *http.Client
	signer  AuthSigner
	logger  *zap.Logger
	authTTL time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithAuthTTL overrides how long signed authorization events stay valid.
func WithAuthTTL(d time.Duration) Option { return func(c *Client) { c.authTTL = d } }

// NewClient creates a Blossom client. The signer authorizes upload/delete
// requests; downloads are unauthenticated (content is encrypted at rest).
func NewClient(signer AuthSigner, logger *zap.Logger, opts ...Option) *Client {
	if logger == nil {
		logger = zap.NewNop()
	}
	c := &Client{
		http: &http.Client{
			Timeout: 30 * time.Second,
			// Blossom blob operations never legitimately redirect. Refusing
			// redirects prevents a user-configured server from bouncing a
			// request to an internal address (SSRF).
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		signer:  signer,
		logger:  logger,
		authTTL: defaultAuthTTL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Upload stores data on up to redundancy servers (in priority order) and
// returns a descriptor of the content-addressed blob. It succeeds if at least
// one server accepts the blob; the error is returned only if all attempts fail.
func (c *Client) Upload(ctx context.Context, data []byte, contentType string, servers []Server, redundancy int) (*BlobDescriptor, error) {
	if len(servers) == 0 {
		return nil, ErrNoServers
	}
	if redundancy < 1 {
		redundancy = 1
	}
	hash := sha256Hex(data)

	auth, err := c.signer.SignAuth(ctx, "upload", []string{hash}, c.expiry())
	if err != nil {
		return nil, fmt.Errorf("blossom: sign upload auth: %w", err)
	}
	header, err := authorizationHeader(auth)
	if err != nil {
		return nil, err
	}

	desc := &BlobDescriptor{SHA256: hash, Size: int64(len(data)), Type: contentType}
	var lastErr error
	for _, srv := range sortedServers(servers) {
		if len(desc.Servers) >= redundancy {
			break
		}
		if err := c.uploadTo(ctx, srv.URL, data, contentType, header); err != nil {
			c.logger.Warn("blossom upload failed", zap.String("server", srv.URL), zap.Error(err))
			lastErr = err
			continue
		}
		desc.Servers = append(desc.Servers, normalizeURL(srv.URL))
	}

	if len(desc.Servers) == 0 {
		return nil, fmt.Errorf("blossom: all uploads failed: %w", lastErr)
	}
	return desc, nil
}

func (c *Client) uploadTo(ctx context.Context, server string, data []byte, contentType, authHeader string) error {
	url := normalizeURL(server) + "/upload"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authHeader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload returned status %d: %s", resp.StatusCode, readReason(resp))
	}
	return nil
}

// Download fetches a blob by hash, trying servers in priority order. The
// returned bytes are verified to hash to the requested address; a server
// returning corrupt/mismatched data is skipped.
func (c *Client) Download(ctx context.Context, hash string, servers []Server) ([]byte, error) {
	if len(servers) == 0 {
		return nil, ErrNoServers
	}
	if !hashPattern.MatchString(hash) {
		return nil, fmt.Errorf("blossom: invalid blob hash %q", hash)
	}
	var lastErr error
	for _, srv := range sortedServers(servers) {
		data, err := c.downloadFrom(ctx, srv.URL, hash)
		if err != nil {
			c.logger.Debug("blossom download miss", zap.String("server", srv.URL), zap.Error(err))
			lastErr = err
			continue
		}
		if got := sha256Hex(data); got != hash {
			lastErr = fmt.Errorf("hash mismatch from %s: want %s got %s", srv.URL, hash, got)
			c.logger.Warn("blossom hash mismatch", zap.String("server", srv.URL))
			continue
		}
		return data, nil
	}
	return nil, fmt.Errorf("blossom: blob %s unavailable: %w", hash, lastErr)
}

func (c *Client) downloadFrom(ctx context.Context, server, hash string) ([]byte, error) {
	url := normalizeURL(server) + "/" + hash
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned status %d", resp.StatusCode)
	}
	// Read one byte past the limit so we can distinguish "exactly at limit"
	// from "over limit" rather than silently truncating (which would surface
	// as a confusing hash mismatch).
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBlobSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBlobSize {
		return nil, ErrBlobTooLarge
	}
	return data, nil
}

// Delete asks every server holding the blob to drop it (BUD-02 garbage
// collection). It is best-effort: an error is returned only if no server
// acknowledged the delete.
func (c *Client) Delete(ctx context.Context, hash string, servers []Server) error {
	if len(servers) == 0 {
		return ErrNoServers
	}
	if !hashPattern.MatchString(hash) {
		return fmt.Errorf("blossom: invalid blob hash %q", hash)
	}
	auth, err := c.signer.SignAuth(ctx, "delete", []string{hash}, c.expiry())
	if err != nil {
		return fmt.Errorf("blossom: sign delete auth: %w", err)
	}
	header, err := authorizationHeader(auth)
	if err != nil {
		return err
	}

	var deleted int
	var lastErr error
	for _, srv := range sortedServers(servers) {
		if err := c.deleteFrom(ctx, srv.URL, hash, header); err != nil {
			c.logger.Warn("blossom delete failed", zap.String("server", srv.URL), zap.Error(err))
			lastErr = err
			continue
		}
		deleted++
	}
	if deleted == 0 {
		return fmt.Errorf("blossom: all deletes failed: %w", lastErr)
	}
	return nil
}

func (c *Client) deleteFrom(ctx context.Context, server, hash, authHeader string) error {
	url := normalizeURL(server) + "/" + hash
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authHeader)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// 404 means the blob is already gone — treat as success for GC purposes.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete returned status %d: %s", resp.StatusCode, readReason(resp))
	}
	return nil
}

func (c *Client) expiry() time.Time { return time.Now().Add(c.authTTL) }

// sortedServers returns a copy ordered by ascending priority.
func sortedServers(servers []Server) []Server {
	out := make([]Server, len(servers))
	copy(out, servers)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

func normalizeURL(u string) string { return strings.TrimRight(u, "/") }

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// readReason returns the X-Reason header (Blossom's error convention) or a
// short body snippet for diagnostics.
func readReason(resp *http.Response) string {
	if r := resp.Header.Get("X-Reason"); r != "" {
		return r
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return strings.TrimSpace(string(body))
}
