package unit

import (
	"context"
	"testing"
	"time"

	"git.aegis-hq.xyz/coldforge/cloistr-email/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDefaultSMTPServerConfig(t *testing.T) {
	config := transport.DefaultSMTPServerConfig()

	assert.Equal(t, ":25", config.ListenAddr)
	assert.Equal(t, "localhost", config.Domain)
	assert.Equal(t, 25*1024*1024, config.MaxMessageSize) // 25MB
	assert.Equal(t, 100, config.MaxRecipients)
	assert.Equal(t, 60*time.Second, config.ReadTimeout)
	assert.Equal(t, 60*time.Second, config.WriteTimeout)
	assert.False(t, config.RequireTLS)
}

func TestSMTPServerConfigFields(t *testing.T) {
	config := &transport.SMTPServerConfig{
		ListenAddr:     ":2525",
		Domain:         "mail.example.com",
		AllowedDomains: []string{"example.com", "test.com"},
		MaxMessageSize: 10 * 1024 * 1024,
		MaxRecipients:  50,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		RequireTLS:     true,
		TLSCertFile:    "/path/to/cert.pem",
		TLSKeyFile:     "/path/to/key.pem",
	}

	assert.Equal(t, ":2525", config.ListenAddr)
	assert.Equal(t, "mail.example.com", config.Domain)
	assert.Len(t, config.AllowedDomains, 2)
	assert.Equal(t, 10*1024*1024, config.MaxMessageSize)
	assert.Equal(t, 50, config.MaxRecipients)
	assert.True(t, config.RequireTLS)
}

func TestNewSMTPServer(t *testing.T) {
	logger := zap.NewNop()

	t.Run("with default config", func(t *testing.T) {
		server := transport.NewSMTPServer(nil, nil, nil, logger)
		require.NotNil(t, server)
		assert.Equal(t, ":25", server.Addr())
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &transport.SMTPServerConfig{
			ListenAddr: ":2525",
			Domain:     "test.example.com",
		}
		server := transport.NewSMTPServer(config, nil, nil, logger)
		require.NotNil(t, server)
		assert.Equal(t, ":2525", server.Addr())
	})
}

func TestSimpleRecipientValidator(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts any domain when no allowed domains configured", func(t *testing.T) {
		validator := &transport.SimpleRecipientValidator{
			AllowedDomains: []string{},
		}

		err := validator.ValidateRecipient(ctx, "user@example.com")
		assert.NoError(t, err)

		err = validator.ValidateRecipient(ctx, "user@any.domain")
		assert.NoError(t, err)
	})

	t.Run("accepts mail for allowed domains", func(t *testing.T) {
		validator := &transport.SimpleRecipientValidator{
			AllowedDomains: []string{"cloistr.xyz", "example.com"},
		}

		err := validator.ValidateRecipient(ctx, "alice@cloistr.xyz")
		assert.NoError(t, err)

		err = validator.ValidateRecipient(ctx, "bob@example.com")
		assert.NoError(t, err)
	})

	t.Run("rejects mail for disallowed domains", func(t *testing.T) {
		validator := &transport.SimpleRecipientValidator{
			AllowedDomains: []string{"cloistr.xyz"},
		}

		err := validator.ValidateRecipient(ctx, "user@other.com")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "domain not accepted")
	})

	t.Run("handles case-insensitive domains", func(t *testing.T) {
		validator := &transport.SimpleRecipientValidator{
			AllowedDomains: []string{"Cloistr.XYZ"},
		}

		err := validator.ValidateRecipient(ctx, "user@cloistr.xyz")
		assert.NoError(t, err)

		err = validator.ValidateRecipient(ctx, "user@CLOISTR.XYZ")
		assert.NoError(t, err)
	})

	t.Run("rejects invalid address format", func(t *testing.T) {
		validator := &transport.SimpleRecipientValidator{
			AllowedDomains: []string{"cloistr.xyz"},
		}

		err := validator.ValidateRecipient(ctx, "invalid-address")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid address format")
	})
}

func TestPermanentError(t *testing.T) {
	t.Run("wraps error", func(t *testing.T) {
		original := assert.AnError
		wrapped := transport.NewPermanentError(original)

		assert.Error(t, wrapped)
		assert.Contains(t, wrapped.Error(), original.Error())
	})
}

// MockMessageHandler implements transport.MessageHandler for testing
type MockMessageHandler struct {
	messages []ReceivedMessage
	err      error
}

type ReceivedMessage struct {
	From string
	To   []string
	Data []byte
}

func (m *MockMessageHandler) HandleMessage(ctx context.Context, from string, to []string, data []byte) error {
	if m.err != nil {
		return m.err
	}
	m.messages = append(m.messages, ReceivedMessage{From: from, To: to, Data: data})
	return nil
}

// Note: Full SMTP session testing requires an actual network connection
// and is better suited for integration tests. These unit tests focus on
// configuration and helper functions.
