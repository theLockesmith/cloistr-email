// Package transport provides abstractions for email delivery mechanisms.
// This allows swapping between SMTP (Stalwart) and future Nostr-native protocols.
package transport

import (
	"context"
	"fmt"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/signing"
	"go.uber.org/zap"
)

// TransportType identifies the delivery mechanism
type TransportType string

const (
	// TransportSMTP uses traditional SMTP via Stalwart
	TransportSMTP TransportType = "smtp"

	// TransportNostr uses Nostr relays (future)
	TransportNostr TransportType = "nostr"

	// TransportHybrid tries Nostr first, falls back to SMTP
	TransportHybrid TransportType = "hybrid"
)

// Message represents an email message ready for transport.
// This is transport-agnostic - the underlying transport converts it
// to the appropriate format (RFC 5322 for SMTP, Nostr event for relays).
type Message struct {
	// Identity fields (who)
	FromAddress string // Traditional email: alice@coldforge.xyz
	ToAddresses []string // Can have multiple recipients
	CCAddresses []string
	BCCAddresses []string

	// Nostr identity (for encryption/signing)
	SenderPubkey    string // Hex pubkey of sender
	RecipientPubkeys map[string]string // email -> hex pubkey mapping

	// Content
	Subject  string
	Body     string // Plaintext body (will be encrypted if needed)
	HTMLBody string // Optional HTML version

	// Encryption state
	// If IsPreEncrypted is true, Body contains ciphertext and server won't encrypt
	// This supports NIP-07 client-side encryption
	IsPreEncrypted bool

	// If EncryptionRequested is true and IsPreEncrypted is false,
	// the transport layer should encrypt using the sender's signer
	EncryptionRequested bool

	// Metadata
	MessageID   string // RFC 5322 Message-ID
	InReplyTo   string // For threading
	References  []string
	Headers     map[string]string // Additional headers

	// Transport preference (can be overridden)
	PreferredTransport TransportType

	// Signer for Nostr signature (optional)
	// If provided, the email will be signed with X-Nostr-* headers
	Signer signing.Signer
}

// DeliveryResult contains the result of a send operation
type DeliveryResult struct {
	// Success indicates if the message was accepted for delivery
	Success bool

	// MessageID assigned by the transport (may differ from input)
	MessageID string

	// Transport that was used
	Transport TransportType

	// Per-recipient results
	Recipients []RecipientResult

	// Error if the entire send failed
	Error error
}

// RecipientResult contains delivery status for a single recipient
type RecipientResult struct {
	Address   string
	Success   bool
	Error     error
	Encrypted bool // Was this recipient's copy encrypted?
}

// Transport defines the interface for email delivery mechanisms.
// Implementations include SMTP (via Stalwart) and future Nostr transport.
type Transport interface {
	// Type returns the transport type identifier
	Type() TransportType

	// Send delivers a message via this transport
	Send(ctx context.Context, msg *Message) (*DeliveryResult, error)

	// CanDeliver checks if this transport can deliver to the given address
	// For SMTP, this is always true. For Nostr, it checks if recipient has npub.
	CanDeliver(ctx context.Context, address string) (bool, error)

	// Health checks if the transport is operational
	Health(ctx context.Context) error
}

// IncomingMessage represents a received email
type IncomingMessage struct {
	// Raw message data
	RawMessage []byte

	// Parsed fields
	MessageID   string
	FromAddress string
	ToAddresses []string
	Subject     string
	Body        string
	HTMLBody    string

	// Encryption metadata (from X-Nostr-* headers)
	IsEncrypted   bool
	SenderPubkey  string
	Algorithm     string

	// Transport it arrived on
	Transport TransportType

	// Stalwart-specific
	StalwartUID string
	Folder      string
}

// Receiver defines the interface for receiving incoming messages.
// This is separate from Transport because receiving has different patterns.
type Receiver interface {
	// Type returns the transport type identifier
	Type() TransportType

	// Receive fetches new messages for a user
	// Returns a channel that will receive messages until context is cancelled
	Receive(ctx context.Context, userEmail string) (<-chan *IncomingMessage, error)

	// Fetch retrieves a specific message by ID
	Fetch(ctx context.Context, userEmail, messageID string) (*IncomingMessage, error)

	// Health checks if the receiver is operational
	Health(ctx context.Context) error
}

// Manager coordinates multiple transports and handles routing
type Manager struct {
	transports map[TransportType]Transport
	receivers  map[TransportType]Receiver
	logger     *zap.Logger

	// Default transport for sending
	defaultTransport TransportType
}

// NewManager creates a new transport manager
func NewManager(logger *zap.Logger) *Manager {
	return &Manager{
		transports:       make(map[TransportType]Transport),
		receivers:        make(map[TransportType]Receiver),
		logger:           logger,
		defaultTransport: TransportSMTP, // Default to SMTP for now
	}
}

// RegisterTransport adds a transport to the manager
func (m *Manager) RegisterTransport(t Transport) {
	m.transports[t.Type()] = t
	m.logger.Info("Registered transport", zap.String("type", string(t.Type())))
}

// RegisterReceiver adds a receiver to the manager
func (m *Manager) RegisterReceiver(r Receiver) {
	m.receivers[r.Type()] = r
	m.logger.Info("Registered receiver", zap.String("type", string(r.Type())))
}

// SetDefaultTransport sets the default transport for sending
func (m *Manager) SetDefaultTransport(t TransportType) {
	m.defaultTransport = t
}

// Send delivers a message using the appropriate transport
func (m *Manager) Send(ctx context.Context, msg *Message) (*DeliveryResult, error) {
	// Determine which transport to use
	transportType := msg.PreferredTransport
	if transportType == "" {
		transportType = m.defaultTransport
	}

	// Handle hybrid routing
	if transportType == TransportHybrid {
		return m.sendHybrid(ctx, msg)
	}

	// Get the transport
	transport, ok := m.transports[transportType]
	if !ok {
		return nil, fmt.Errorf("transport not available: %s", transportType)
	}

	return transport.Send(ctx, msg)
}

// sendHybrid tries Nostr first, falls back to SMTP
func (m *Manager) sendHybrid(ctx context.Context, msg *Message) (*DeliveryResult, error) {
	// Try Nostr if available
	if nostrTransport, ok := m.transports[TransportNostr]; ok {
		// Check if all recipients support Nostr
		allSupported := true
		for _, addr := range msg.ToAddresses {
			canDeliver, _ := nostrTransport.CanDeliver(ctx, addr)
			if !canDeliver {
				allSupported = false
				break
			}
		}

		if allSupported {
			result, err := nostrTransport.Send(ctx, msg)
			if err == nil && result.Success {
				return result, nil
			}
			m.logger.Debug("Nostr delivery failed, falling back to SMTP", zap.Error(err))
		}
	}

	// Fall back to SMTP
	smtpTransport, ok := m.transports[TransportSMTP]
	if !ok {
		return nil, fmt.Errorf("no fallback transport available")
	}

	return smtpTransport.Send(ctx, msg)
}

// GetTransport returns a specific transport by type
func (m *Manager) GetTransport(t TransportType) (Transport, bool) {
	transport, ok := m.transports[t]
	return transport, ok
}

// GetReceiver returns a specific receiver by type
func (m *Manager) GetReceiver(t TransportType) (Receiver, bool) {
	receiver, ok := m.receivers[t]
	return receiver, ok
}

// Health checks all registered transports
func (m *Manager) Health(ctx context.Context) map[TransportType]error {
	results := make(map[TransportType]error)
	for t, transport := range m.transports {
		results[t] = transport.Health(ctx)
	}
	return results
}
