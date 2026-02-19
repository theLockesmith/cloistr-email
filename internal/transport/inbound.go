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
	config  *SMTPServerConfig
	server  *smtp.Server
	handler MessageHandler
	validator RecipientValidator
	logger  *zap.Logger

	mu      sync.Mutex
	running bool
}

// NewSMTPServer creates a new inbound SMTP server
func NewSMTPServer(config *SMTPServerConfig, handler MessageHandler, validator RecipientValidator, logger *zap.Logger) *SMTPServer {
	if config == nil {
		config = DefaultSMTPServerConfig()
	}

	s := &SMTPServer{
		config:    config,
		handler:   handler,
		validator: validator,
		logger:    logger,
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
	b.logger.Debug("New SMTP session",
		zap.String("remote_addr", c.Conn().RemoteAddr().String()))

	return &smtpSession{
		backend: b,
		conn:    c,
		logger:  b.logger,
	}, nil
}

// smtpSession implements smtp.Session
type smtpSession struct {
	backend *smtpBackend
	conn    *smtp.Conn
	logger  *zap.Logger

	from string
	to   []string
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

	// Process the message
	if s.backend.server.handler != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := s.backend.server.handler.HandleMessage(ctx, s.from, s.to, buf.Bytes()); err != nil {
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
		zap.Int64("size", n))

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
