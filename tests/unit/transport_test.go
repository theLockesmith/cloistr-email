package unit

import (
	"context"
	"testing"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/transport"
	"go.uber.org/zap"
)

func TestTransportManager(t *testing.T) {
	logger := zap.NewNop()

	t.Run("NewManager creates empty manager", func(t *testing.T) {
		mgr := transport.NewManager(logger)
		if mgr == nil {
			t.Fatal("Expected non-nil manager")
		}
	})

	t.Run("GetTransport returns false for unregistered transport", func(t *testing.T) {
		mgr := transport.NewManager(logger)
		_, ok := mgr.GetTransport(transport.TransportSMTP)
		if ok {
			t.Error("Expected false for unregistered transport")
		}
	})

	t.Run("RegisterTransport and GetTransport", func(t *testing.T) {
		mgr := transport.NewManager(logger)
		mockTransport := &MockTransport{transportType: transport.TransportSMTP}
		mgr.RegisterTransport(mockTransport)

		retrieved, ok := mgr.GetTransport(transport.TransportSMTP)
		if !ok {
			t.Fatal("Expected transport to be registered")
		}
		if retrieved.Type() != transport.TransportSMTP {
			t.Errorf("Expected SMTP transport type, got %s", retrieved.Type())
		}
	})

	t.Run("SetDefaultTransport", func(t *testing.T) {
		mgr := transport.NewManager(logger)
		mgr.SetDefaultTransport(transport.TransportNostr)
		// No way to verify directly without sending, but at least it doesn't panic
	})
}

func TestMessage(t *testing.T) {
	t.Run("Message struct fields", func(t *testing.T) {
		msg := &transport.Message{
			FromAddress:         "alice@coldforge.xyz",
			ToAddresses:         []string{"bob@example.com"},
			CCAddresses:         []string{"carol@example.com"},
			BCCAddresses:        []string{"dave@example.com"},
			SenderPubkey:        "abc123",
			RecipientPubkeys:    map[string]string{"bob@example.com": "def456"},
			Subject:             "Test Subject",
			Body:                "Test Body",
			HTMLBody:            "<p>Test Body</p>",
			IsPreEncrypted:      false,
			EncryptionRequested: true,
			MessageID:           "msg123@coldforge.xyz",
			InReplyTo:           "original@example.com",
			References:          []string{"ref1@example.com", "ref2@example.com"},
			PreferredTransport:  transport.TransportSMTP,
		}

		if msg.FromAddress != "alice@coldforge.xyz" {
			t.Errorf("FromAddress mismatch: %s", msg.FromAddress)
		}
		if len(msg.ToAddresses) != 1 {
			t.Errorf("Expected 1 ToAddress, got %d", len(msg.ToAddresses))
		}
		if msg.Subject != "Test Subject" {
			t.Errorf("Subject mismatch: %s", msg.Subject)
		}
	})
}

func TestDeliveryResult(t *testing.T) {
	t.Run("DeliveryResult struct", func(t *testing.T) {
		result := &transport.DeliveryResult{
			Success:   true,
			MessageID: "msg123",
			Transport: transport.TransportSMTP,
			Recipients: []transport.RecipientResult{
				{
					Address:   "bob@example.com",
					Success:   true,
					Encrypted: true,
				},
			},
		}

		if !result.Success {
			t.Error("Expected success to be true")
		}
		if result.Transport != transport.TransportSMTP {
			t.Errorf("Expected SMTP transport, got %s", result.Transport)
		}
		if len(result.Recipients) != 1 {
			t.Errorf("Expected 1 recipient, got %d", len(result.Recipients))
		}
	})
}

func TestTransportTypes(t *testing.T) {
	t.Run("TransportType constants", func(t *testing.T) {
		if transport.TransportSMTP != "smtp" {
			t.Errorf("TransportSMTP should be 'smtp', got %s", transport.TransportSMTP)
		}
		if transport.TransportNostr != "nostr" {
			t.Errorf("TransportNostr should be 'nostr', got %s", transport.TransportNostr)
		}
		if transport.TransportHybrid != "hybrid" {
			t.Errorf("TransportHybrid should be 'hybrid', got %s", transport.TransportHybrid)
		}
	})
}

func TestSMTPTransportCanDeliver(t *testing.T) {
	logger := zap.NewNop()
	config := &transport.SMTPConfig{
		Host: "localhost",
		Port: 587,
	}
	smtpTransport := transport.NewSMTPTransport(config, nil, logger)

	tests := []struct {
		address  string
		expected bool
	}{
		{"user@example.com", true},
		{"alice@coldforge.xyz", true},
		{"user@sub.domain.com", true},
		{"invalid", false},
		{"@example.com", false},
		{"user@", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			canDeliver, err := smtpTransport.CanDeliver(context.Background(), tt.address)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if canDeliver != tt.expected {
				t.Errorf("CanDeliver(%s) = %v, expected %v", tt.address, canDeliver, tt.expected)
			}
		})
	}
}

func TestSMTPTransportType(t *testing.T) {
	logger := zap.NewNop()
	config := &transport.SMTPConfig{
		Host: "localhost",
		Port: 587,
	}
	smtpTransport := transport.NewSMTPTransport(config, nil, logger)

	if smtpTransport.Type() != transport.TransportSMTP {
		t.Errorf("Expected SMTP type, got %s", smtpTransport.Type())
	}
}

// MockTransport implements transport.Transport for testing
type MockTransport struct {
	transportType transport.TransportType
	sendFunc      func(ctx context.Context, msg *transport.Message) (*transport.DeliveryResult, error)
	canDeliverFn  func(ctx context.Context, address string) (bool, error)
	healthFunc    func(ctx context.Context) error
}

func (m *MockTransport) Type() transport.TransportType {
	return m.transportType
}

func (m *MockTransport) Send(ctx context.Context, msg *transport.Message) (*transport.DeliveryResult, error) {
	if m.sendFunc != nil {
		return m.sendFunc(ctx, msg)
	}
	return &transport.DeliveryResult{
		Success:   true,
		MessageID: "mock-message-id",
		Transport: m.transportType,
	}, nil
}

func (m *MockTransport) CanDeliver(ctx context.Context, address string) (bool, error) {
	if m.canDeliverFn != nil {
		return m.canDeliverFn(ctx, address)
	}
	return true, nil
}

func (m *MockTransport) Health(ctx context.Context) error {
	if m.healthFunc != nil {
		return m.healthFunc(ctx)
	}
	return nil
}

func TestManagerSend(t *testing.T) {
	logger := zap.NewNop()

	t.Run("Send with default transport", func(t *testing.T) {
		mgr := transport.NewManager(logger)
		mockTransport := &MockTransport{
			transportType: transport.TransportSMTP,
			sendFunc: func(ctx context.Context, msg *transport.Message) (*transport.DeliveryResult, error) {
				return &transport.DeliveryResult{
					Success:   true,
					MessageID: "test-msg-id",
					Transport: transport.TransportSMTP,
				}, nil
			},
		}
		mgr.RegisterTransport(mockTransport)

		msg := &transport.Message{
			FromAddress: "alice@coldforge.xyz",
			ToAddresses: []string{"bob@example.com"},
			Subject:     "Test",
			Body:        "Test body",
		}

		result, err := mgr.Send(context.Background(), msg)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Error("Expected success")
		}
		if result.Transport != transport.TransportSMTP {
			t.Errorf("Expected SMTP transport, got %s", result.Transport)
		}
	})

	t.Run("Send with unavailable transport", func(t *testing.T) {
		mgr := transport.NewManager(logger)
		// Don't register any transport

		msg := &transport.Message{
			FromAddress: "alice@coldforge.xyz",
			ToAddresses: []string{"bob@example.com"},
			Subject:     "Test",
			Body:        "Test body",
		}

		_, err := mgr.Send(context.Background(), msg)
		if err == nil {
			t.Fatal("Expected error for unavailable transport")
		}
	})

	t.Run("Send with preferred transport", func(t *testing.T) {
		mgr := transport.NewManager(logger)
		smtpTransport := &MockTransport{transportType: transport.TransportSMTP}
		nostrTransport := &MockTransport{
			transportType: transport.TransportNostr,
			sendFunc: func(ctx context.Context, msg *transport.Message) (*transport.DeliveryResult, error) {
				return &transport.DeliveryResult{
					Success:   true,
					MessageID: "nostr-msg-id",
					Transport: transport.TransportNostr,
				}, nil
			},
		}
		mgr.RegisterTransport(smtpTransport)
		mgr.RegisterTransport(nostrTransport)

		msg := &transport.Message{
			FromAddress:        "alice@coldforge.xyz",
			ToAddresses:        []string{"bob@example.com"},
			Subject:            "Test",
			Body:               "Test body",
			PreferredTransport: transport.TransportNostr,
		}

		result, err := mgr.Send(context.Background(), msg)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Transport != transport.TransportNostr {
			t.Errorf("Expected Nostr transport, got %s", result.Transport)
		}
	})
}

func TestManagerHealth(t *testing.T) {
	logger := zap.NewNop()

	t.Run("Health with registered transports", func(t *testing.T) {
		mgr := transport.NewManager(logger)
		healthyCalled := false
		mockTransport := &MockTransport{
			transportType: transport.TransportSMTP,
			healthFunc: func(ctx context.Context) error {
				healthyCalled = true
				return nil
			},
		}
		mgr.RegisterTransport(mockTransport)

		results := mgr.Health(context.Background())
		if !healthyCalled {
			t.Error("Expected health check to be called")
		}
		if err, ok := results[transport.TransportSMTP]; !ok || err != nil {
			t.Errorf("Expected healthy SMTP transport, got error: %v", err)
		}
	})
}
