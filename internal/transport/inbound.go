// Package transport provides email transport mechanisms.
package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-smtp"
	"go.uber.org/zap"
)

// loadTLSCert loads a TLS certificate from files
func loadTLSCert(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
	}, nil
}

// SMTPServerConfig contains configuration for the inbound SMTP server
type SMTPServerConfig struct {
	// ListenAddr is the address to listen on (e.g., ":25" or "0.0.0.0:25")
	ListenAddr string

	// Domain is the server's hostname for SMTP HELO/EHLO
	Domain string

	// AllowedDomains are the domains we accept mail for
	// If empty, accepts mail for any domain
	AllowedDomains []string

	// MaxMessageSize is the maximum message size in bytes (default: 25MB)
	MaxMessageSize int

	// MaxRecipients is the maximum number of recipients per message (default: 100)
	MaxRecipients int

	// ReadTimeout is the timeout for reading from connections (default: 60s)
	ReadTimeout time.Duration

	// WriteTimeout is the timeout for writing to connections (default: 60s)
	WriteTimeout time.Duration

	// RequireTLS requires STARTTLS before accepting mail
	RequireTLS bool

	// TLSCertFile is the path to the TLS certificate file
	TLSCertFile string

	// TLSKeyFile is the path to the TLS key file
	TLSKeyFile string

	// EnableSPF enables SPF validation for incoming mail
	EnableSPF bool

	// SPFFailAction determines what happens when SPF fails
	// "reject" = reject the message, "tag" = accept but tag, "none" = ignore
	SPFFailAction string

	// EnableDKIM enables DKIM signature verification for incoming mail
	EnableDKIM bool

	// DKIMFailAction determines what happens when DKIM fails
	// "reject" = reject the message, "tag" = accept but tag, "none" = ignore
	DKIMFailAction string
}

// DefaultSMTPServerConfig returns sensible defaults for the SMTP server
func DefaultSMTPServerConfig() *SMTPServerConfig {
	return &SMTPServerConfig{
		ListenAddr:     ":25",
		Domain:         "localhost",
		MaxMessageSize: 25 * 1024 * 1024, // 25MB
		MaxRecipients:  100,
		ReadTimeout:    60 * time.Second,
		WriteTimeout:   60 * time.Second,
		RequireTLS:     false,
	}
}

// MessageHandler processes incoming email messages
type MessageHandler interface {
	// HandleMessage is called when a complete message has been received
	// from is the envelope sender, to is the list of envelope recipients
	HandleMessage(ctx context.Context, from string, to []string, data []byte) error
}

// RecipientValidator validates if we should accept mail for a recipient
type RecipientValidator interface {
	// ValidateRecipient checks if we accept mail for the given address
	// Returns nil if valid, error otherwise
	ValidateRecipient(ctx context.Context, address string) error
}

// SMTPServer is an inbound SMTP server using emersion/go-smtp
type SMTPServer struct {
	config       *SMTPServerConfig
	server       *smtp.Server
	handler      MessageHandler
	validator    RecipientValidator
	rateLimiter  *RateLimiter
	spfValidator *SPFValidator
	dkimVerifier *DKIMVerifier
	bounceHandler *BounceHandler
	logger       *zap.Logger

	mu      sync.Mutex
	running bool
}

// SMTPServerOption configures an SMTPServer
type SMTPServerOption func(*SMTPServer)

// WithRateLimiter adds rate limiting to the SMTP server
func WithRateLimiter(rl *RateLimiter) SMTPServerOption {
	return func(s *SMTPServer) {
		s.rateLimiter = rl
	}
}

// WithSPFValidator adds SPF validation to the SMTP server
func WithSPFValidator(v *SPFValidator) SMTPServerOption {
	return func(s *SMTPServer) {
		s.spfValidator = v
	}
}

// WithDKIMVerifier adds DKIM verification to the SMTP server
func WithDKIMVerifier(v *DKIMVerifier) SMTPServerOption {
	return func(s *SMTPServer) {
		s.dkimVerifier = v
	}
}

// WithBounceHandler adds bounce handling to the SMTP server
func WithBounceHandler(h *BounceHandler) SMTPServerOption {
	return func(s *SMTPServer) {
		s.bounceHandler = h
	}
}

// NewSMTPServer creates a new inbound SMTP server
func NewSMTPServer(config *SMTPServerConfig, handler MessageHandler, validator RecipientValidator, logger *zap.Logger, opts ...SMTPServerOption) *SMTPServer {
	if config == nil {
		config = DefaultSMTPServerConfig()
	}

	s := &SMTPServer{
		config:    config,
		handler:   handler,
		validator: validator,
		logger:    logger,
	}

	// Apply options
	for _, opt := range opts {
		opt(s)
	}

	// Create the backend
	backend := &smtpBackend{
		server: s,
		logger: logger,
	}

	// Create the SMTP server
	server := smtp.NewServer(backend)
	server.Addr = config.ListenAddr
	server.Domain = config.Domain
	server.MaxMessageBytes = int64(config.MaxMessageSize)
	server.MaxRecipients = config.MaxRecipients
	server.ReadTimeout = config.ReadTimeout
	server.WriteTimeout = config.WriteTimeout
	server.AllowInsecureAuth = !config.RequireTLS

	s.server = server

	return s
}

// Start starts the SMTP server
func (s *SMTPServer) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("server already running")
	}
	s.running = true
	s.mu.Unlock()

	s.logger.Info("Starting inbound SMTP server",
		zap.String("addr", s.config.ListenAddr),
		zap.String("domain", s.config.Domain))

	// Start with or without TLS
	if s.config.TLSCertFile != "" && s.config.TLSKeyFile != "" {
		// Load TLS config
		cert, err := loadTLSCert(s.config.TLSCertFile, s.config.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("failed to load TLS certificate: %w", err)
		}
		s.server.TLSConfig = cert
		return s.server.ListenAndServeTLS()
	}

	return s.server.ListenAndServe()
}

// Stop gracefully stops the SMTP server
func (s *SMTPServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.logger.Info("Stopping inbound SMTP server")
	s.running = false
	return s.server.Close()
}

// Addr returns the server's listen address
func (s *SMTPServer) Addr() string {
	return s.config.ListenAddr
}

// smtpBackend implements smtp.Backend
type smtpBackend struct {
	server *SMTPServer
	logger *zap.Logger
}

// NewSession implements smtp.Backend
func (b *smtpBackend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	remoteAddr := c.Conn().RemoteAddr().String()
	clientIP := extractIP(remoteAddr)

	b.logger.Debug("New SMTP session",
		zap.String("remote_addr", remoteAddr),
		zap.String("client_ip", clientIP))

	// Check rate limits for connection
	if b.server.rateLimiter != nil {
		if err := b.server.rateLimiter.AllowConnection(clientIP); err != nil {
			b.logger.Warn("Connection rate limited",
				zap.String("client_ip", clientIP),
				zap.Error(err))
			return nil, &smtp.SMTPError{
				Code:         421,
				EnhancedCode: smtp.EnhancedCode{4, 7, 0},
				Message:      "Too many connections, please try again later",
			}
		}
	}

	return &smtpSession{
		backend:  b,
		conn:     c,
		clientIP: clientIP,
		logger:   b.logger,
	}, nil
}

// extractIP extracts the IP address from a remote address string
func extractIP(addr string) string {
	// Handle IPv6 addresses like [::1]:1234
	if idx := strings.LastIndex(addr, "]:"); idx != -1 {
		return strings.TrimPrefix(addr[:idx+1], "[")
	}
	// Handle IPv4 addresses like 192.168.1.1:1234
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// smtpSession implements smtp.Session
type smtpSession struct {
	backend  *smtpBackend
	conn     *smtp.Conn
	clientIP string
	logger   *zap.Logger

	from       string
	fromDomain string
	to         []string
	spfResult  *SPFCheckResult
}

// Mail implements smtp.Session - handles MAIL FROM command
func (s *smtpSession) Mail(from string, opts *smtp.MailOptions) error {
	s.logger.Debug("MAIL FROM", zap.String("from", from))

	// Basic validation of sender address
	if from == "" {
		// Empty sender is allowed for bounces (RFC 5321)
		s.from = ""
		return nil
	}

	// Parse and validate the address format
	addr, err := mail.ParseAddress(from)
	if err != nil {
		// Try with angle brackets
		addr, err = mail.ParseAddress("<" + from + ">")
		if err != nil {
			s.logger.Debug("Invalid sender address", zap.String("from", from), zap.Error(err))
			return &smtp.SMTPError{
				Code:         553,
				EnhancedCode: smtp.EnhancedCode{5, 1, 3},
				Message:      "Invalid sender address",
			}
		}
	}

	s.from = addr.Address

	// Extract domain for SPF check
	parts := strings.Split(s.from, "@")
	if len(parts) == 2 {
		s.fromDomain = parts[1]
	}

	// Perform SPF validation if enabled
	if s.backend.server.spfValidator != nil && s.backend.server.config.EnableSPF && s.fromDomain != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		s.spfResult = s.backend.server.spfValidator.Check(ctx, s.clientIP, s.fromDomain, s.from)
		s.logger.Debug("SPF check result",
			zap.String("from", s.from),
			zap.String("domain", s.fromDomain),
			zap.String("client_ip", s.clientIP),
			zap.String("result", string(s.spfResult.Result)),
			zap.String("explanation", s.spfResult.Explanation))

		// Handle SPF failure based on policy
		if s.spfResult.Result == SPFFail && s.backend.server.config.SPFFailAction == "reject" {
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 7, 23},
				Message:      fmt.Sprintf("SPF validation failed: %s", s.spfResult.Explanation),
			}
		}
	}

	return nil
}

// Rcpt implements smtp.Session - handles RCPT TO command
func (s *smtpSession) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.logger.Debug("RCPT TO", zap.String("to", to))

	// Parse the address
	addr, err := mail.ParseAddress(to)
	if err != nil {
		// Try with angle brackets
		addr, err = mail.ParseAddress("<" + to + ">")
		if err != nil {
			return &smtp.SMTPError{
				Code:         553,
				EnhancedCode: smtp.EnhancedCode{5, 1, 3},
				Message:      "Invalid recipient address",
			}
		}
	}

	recipient := addr.Address

	// Check if we accept mail for this domain
	if len(s.backend.server.config.AllowedDomains) > 0 {
		parts := strings.Split(recipient, "@")
		if len(parts) != 2 {
			return &smtp.SMTPError{
				Code:         553,
				EnhancedCode: smtp.EnhancedCode{5, 1, 3},
				Message:      "Invalid recipient address format",
			}
		}

		domain := strings.ToLower(parts[1])
		allowed := false
		for _, d := range s.backend.server.config.AllowedDomains {
			if strings.ToLower(d) == domain {
				allowed = true
				break
			}
		}

		if !allowed {
			s.logger.Debug("Recipient domain not allowed",
				zap.String("recipient", recipient),
				zap.String("domain", domain))
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 1, 1},
				Message:      "Recipient domain not accepted",
			}
		}
	}

	// Validate the recipient exists (if validator is configured)
	if s.backend.server.validator != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.backend.server.validator.ValidateRecipient(ctx, recipient); err != nil {
			s.logger.Debug("Recipient validation failed",
				zap.String("recipient", recipient),
				zap.Error(err))
			return &smtp.SMTPError{
				Code:         550,
				EnhancedCode: smtp.EnhancedCode{5, 1, 1},
				Message:      "User unknown",
			}
		}
	}

	s.to = append(s.to, recipient)
	return nil
}

// Data implements smtp.Session - handles DATA command
func (s *smtpSession) Data(r io.Reader) error {
	s.logger.Debug("DATA", zap.String("from", s.from), zap.Strings("to", s.to))

	// Check rate limits for message
	if s.backend.server.rateLimiter != nil {
		if err := s.backend.server.rateLimiter.AllowMessage(s.clientIP, len(s.to)); err != nil {
			s.logger.Warn("Message rate limited",
				zap.String("client_ip", s.clientIP),
				zap.Error(err))
			return &smtp.SMTPError{
				Code:         451,
				EnhancedCode: smtp.EnhancedCode{4, 7, 0},
				Message:      "Too many messages, please try again later",
			}
		}
	}

	// Read the message data
	var buf bytes.Buffer
	maxSize := int64(s.backend.server.config.MaxMessageSize)
	limitedReader := io.LimitReader(r, maxSize+1)

	n, err := buf.ReadFrom(limitedReader)
	if err != nil {
		s.logger.Error("Failed to read message data", zap.Error(err))
		return &smtp.SMTPError{
			Code:         451,
			EnhancedCode: smtp.EnhancedCode{4, 3, 0},
			Message:      "Error reading message",
		}
	}

	if n > maxSize {
		return &smtp.SMTPError{
			Code:         552,
			EnhancedCode: smtp.EnhancedCode{5, 3, 4},
			Message:      "Message too large",
		}
	}

	data := buf.Bytes()

	// Check if this is a bounce message
	if s.backend.server.bounceHandler != nil && s.backend.server.bounceHandler.IsBounce(s.from, data) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := s.backend.server.bounceHandler.ProcessBounce(ctx, s.from, s.to, data); err != nil {
			s.logger.Error("Failed to process bounce",
				zap.String("from", s.from),
				zap.Error(err))
		} else {
			s.logger.Info("Bounce message processed",
				zap.String("from", s.from),
				zap.Strings("to", s.to))
			return nil // Bounce handled, don't process as regular message
		}
	}

	// Perform DKIM verification if enabled
	var dkimResult *DKIMVerificationResult
	if s.backend.server.dkimVerifier != nil && s.backend.server.config.EnableDKIM {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		dkimResult = s.backend.server.dkimVerifier.Verify(ctx, data)
		cancel()

		s.logger.Debug("DKIM verification result",
			zap.Bool("valid", dkimResult.Valid),
			zap.Int("signatures", len(dkimResult.Signatures)),
			zap.String("error", dkimResult.Error))

		// Handle DKIM failure based on policy
		if !dkimResult.Valid && s.backend.server.config.DKIMFailAction == "reject" {
			// Only reject if there were signatures that failed verification
			// Don't reject unsigned mail
			if len(dkimResult.Signatures) > 0 {
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 7, 20},
					Message:      fmt.Sprintf("DKIM verification failed: %s", dkimResult.Error),
				}
			}
		}
	}

	// Process the message
	if s.backend.server.handler != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Add verification results to context
		ctx = contextWithVerificationResults(ctx, s.spfResult, dkimResult)

		if err := s.backend.server.handler.HandleMessage(ctx, s.from, s.to, data); err != nil {
			s.logger.Error("Failed to handle message",
				zap.String("from", s.from),
				zap.Strings("to", s.to),
				zap.Error(err))

			// Determine if it's a permanent or temporary failure
			if isPermanentError(err) {
				return &smtp.SMTPError{
					Code:         550,
					EnhancedCode: smtp.EnhancedCode{5, 0, 0},
					Message:      "Message rejected",
				}
			}

			return &smtp.SMTPError{
				Code:         451,
				EnhancedCode: smtp.EnhancedCode{4, 0, 0},
				Message:      "Temporary failure, please retry",
			}
		}
	}

	s.logger.Info("Message received",
		zap.String("from", s.from),
		zap.Strings("to", s.to),
		zap.Int64("size", n),
		zap.String("spf", s.spfResultString()),
		zap.String("dkim", dkimResultString(dkimResult)))

	return nil
}

// spfResultString returns a string representation of the SPF result
func (s *smtpSession) spfResultString() string {
	if s.spfResult == nil {
		return "none"
	}
	return string(s.spfResult.Result)
}

// dkimResultString returns a string representation of the DKIM result
func dkimResultString(r *DKIMVerificationResult) string {
	if r == nil {
		return "none"
	}
	if r.Valid {
		return "pass"
	}
	if len(r.Signatures) == 0 {
		return "none"
	}
	return "fail"
}

// Context key for SPF result
type spfResultKeyType string

const spfResultKey spfResultKeyType = "spf_result"

// contextWithVerificationResults adds verification results to a context
func contextWithVerificationResults(ctx context.Context, spf *SPFCheckResult, dkim *DKIMVerificationResult) context.Context {
	if spf != nil {
		ctx = context.WithValue(ctx, spfResultKey, spf)
	}
	if dkim != nil {
		// dkimResultKey is defined in dkim_verify.go
		ctx = context.WithValue(ctx, dkimResultKeyType("dkim_result"), dkim)
	}
	return ctx
}

// GetSPFResult retrieves the SPF result from a context
func GetSPFResult(ctx context.Context) *SPFCheckResult {
	if v := ctx.Value(spfResultKey); v != nil {
		return v.(*SPFCheckResult)
	}
	return nil
}

// Reset implements smtp.Session
func (s *smtpSession) Reset() {
	s.from = ""
	s.to = nil
}

// Logout implements smtp.Session
func (s *smtpSession) Logout() error {
	s.logger.Debug("SMTP session closed")
	return nil
}

// PermanentError marks an error as permanent (5xx)
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string {
	return e.Err.Error()
}

func (e *PermanentError) Unwrap() error {
	return e.Err
}

// NewPermanentError wraps an error as a permanent error
func NewPermanentError(err error) error {
	return &PermanentError{Err: err}
}

// isPermanentError checks if an error should result in a 5xx response
func isPermanentError(err error) bool {
	var permErr *PermanentError
	return errors.As(err, &permErr)
}

// SimpleRecipientValidator validates recipients against a list of allowed domains
type SimpleRecipientValidator struct {
	AllowedDomains []string
}

// ValidateRecipient implements RecipientValidator
func (v *SimpleRecipientValidator) ValidateRecipient(ctx context.Context, address string) error {
	parts := strings.Split(address, "@")
	if len(parts) != 2 {
		return fmt.Errorf("invalid address format")
	}

	if len(v.AllowedDomains) == 0 {
		return nil
	}

	domain := strings.ToLower(parts[1])
	for _, d := range v.AllowedDomains {
		if strings.ToLower(d) == domain {
			return nil
		}
	}

	return fmt.Errorf("domain not accepted: %s", domain)
}
