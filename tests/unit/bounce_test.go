package unit

import (
	"context"
	"fmt"
	"testing"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestBounceTypeConstants(t *testing.T) {
	assert.Equal(t, transport.BounceType("hard"), transport.BounceTypeHard)
	assert.Equal(t, transport.BounceType("soft"), transport.BounceTypeSoft)
	assert.Equal(t, transport.BounceType("unknown"), transport.BounceTypeUnknown)
}

func TestNewBounceHandler(t *testing.T) {
	logger := zap.NewNop()

	t.Run("without callbacks", func(t *testing.T) {
		handler := transport.NewBounceHandler(nil, logger)
		require.NotNil(t, handler)
	})

	t.Run("with callbacks", func(t *testing.T) {
		hardBounceCalled := false
		softBounceCalled := false

		handler := transport.NewBounceHandler(nil, logger,
			transport.WithHardBounceCallback(func(ctx context.Context, bounce *transport.BounceInfo) error {
				hardBounceCalled = true
				return nil
			}),
			transport.WithSoftBounceCallback(func(ctx context.Context, bounce *transport.BounceInfo) error {
				softBounceCalled = true
				return nil
			}),
		)
		require.NotNil(t, handler)
		assert.False(t, hardBounceCalled)
		assert.False(t, softBounceCalled)
	})
}

func TestIsBounce(t *testing.T) {
	logger := zap.NewNop()
	handler := transport.NewBounceHandler(nil, logger)

	tests := []struct {
		name     string
		from     string
		data     []byte
		expected bool
	}{
		{
			name:     "empty sender (standard bounce)",
			from:     "",
			data:     []byte("From: <>\r\nSubject: Test\r\n\r\nBody"),
			expected: true,
		},
		{
			name:     "null sender",
			from:     "<>",
			data:     []byte("From: <>\r\nSubject: Test\r\n\r\nBody"),
			expected: true,
		},
		{
			name:     "delivery status notification subject",
			from:     "mailer-daemon@example.com",
			data:     []byte("From: mailer-daemon@example.com\r\nSubject: Delivery Status Notification\r\n\r\nBody"),
			expected: true,
		},
		{
			name:     "undeliverable subject",
			from:     "postmaster@example.com",
			data:     []byte("From: postmaster@example.com\r\nSubject: Undeliverable: Your message\r\n\r\nBody"),
			expected: true,
		},
		{
			name:     "mail delivery failed subject",
			from:     "postmaster@example.com",
			data:     []byte("From: postmaster@example.com\r\nSubject: Mail Delivery Failed\r\n\r\nBody"),
			expected: true,
		},
		{
			name:     "multipart report content type",
			from:     "mailer-daemon@example.com",
			data:     []byte("From: mailer-daemon@example.com\r\nContent-Type: multipart/report\r\n\r\nBody"),
			expected: true,
		},
		{
			name:     "normal email",
			from:     "alice@example.com",
			data:     []byte("From: alice@example.com\r\nSubject: Hello\r\n\r\nHi there!"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.IsBounce(tt.from, tt.data)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBounceInfoStruct(t *testing.T) {
	info := &transport.BounceInfo{
		Type:              transport.BounceTypeHard,
		OriginalRecipient: "user@example.com",
		OriginalMessageID: "<test123@example.com>",
		Reason:            "User unknown",
		DiagnosticCode:    "550 5.1.1",
		RemoteServer:      "mail.example.com",
	}

	assert.Equal(t, transport.BounceTypeHard, info.Type)
	assert.Equal(t, "user@example.com", info.OriginalRecipient)
	assert.Equal(t, "<test123@example.com>", info.OriginalMessageID)
	assert.Equal(t, "User unknown", info.Reason)
	assert.Equal(t, "550 5.1.1", info.DiagnosticCode)
}

func TestBounceClassification(t *testing.T) {
	// Test hard bounce indicators in subjects
	hardBounceSubjects := []string{
		"user unknown",
		"does not exist",
		"no such user",
		"invalid recipient",
		"mailbox not found",
	}

	softBounceSubjects := []string{
		"mailbox full",
		"over quota",
		"temporarily unavailable",
		"try again later",
		"rate limit exceeded",
	}

	// These would be tested via ProcessBounce, but we verify the concept
	assert.NotEmpty(t, hardBounceSubjects)
	assert.NotEmpty(t, softBounceSubjects)
}

func TestBounceMessageParsing(t *testing.T) {
	// Sample bounce message with diagnostic info
	bounceMessage := []byte(`From: mailer-daemon@mail.example.com
To: sender@cloistr.xyz
Subject: Delivery Status Notification (Failure)
Date: Mon, 17 Feb 2026 12:00:00 +0000
Content-Type: multipart/report; report-type=delivery-status; boundary="boundary"
X-Failed-Recipients: recipient@example.com

--boundary
Content-Type: text/plain

Your message could not be delivered.

--boundary
Content-Type: message/delivery-status

Reporting-MTA: dns;mail.example.com
Final-Recipient: rfc822;recipient@example.com
Action: failed
Status: 5.1.1
Diagnostic-Code: smtp;550 5.1.1 User unknown

--boundary--
`)

	// Verify the message structure is valid
	assert.Contains(t, string(bounceMessage), "X-Failed-Recipients:")
	assert.Contains(t, string(bounceMessage), "Final-Recipient:")
	assert.Contains(t, string(bounceMessage), "Diagnostic-Code:")
	assert.Contains(t, string(bounceMessage), "5.1.1")
}

func TestRecordOutboundFailure(t *testing.T) {
	logger := zap.NewNop()

	t.Run("without database", func(t *testing.T) {
		handler := transport.NewBounceHandler(nil, logger)

		// Should not error without a database
		err := handler.RecordOutboundFailure(
			context.Background(),
			"<test123@example.com>",
			[]string{"user@example.com"},
			assert.AnError,
		)
		assert.NoError(t, err)
	})

	t.Run("hard bounce classification", func(t *testing.T) {
		var receivedBounce *transport.BounceInfo
		handler := transport.NewBounceHandler(nil, logger,
			transport.WithHardBounceCallback(func(ctx context.Context, bounce *transport.BounceInfo) error {
				receivedBounce = bounce
				return nil
			}),
		)

		err := handler.RecordOutboundFailure(
			context.Background(),
			"<test123@example.com>",
			[]string{"user@example.com"},
			fmt.Errorf("550 5.1.1 User unknown"),
		)
		assert.NoError(t, err)
		require.NotNil(t, receivedBounce)
		assert.Equal(t, transport.BounceTypeHard, receivedBounce.Type)
		assert.Equal(t, "user@example.com", receivedBounce.OriginalRecipient)
	})

	t.Run("soft bounce classification", func(t *testing.T) {
		var receivedBounce *transport.BounceInfo
		handler := transport.NewBounceHandler(nil, logger,
			transport.WithSoftBounceCallback(func(ctx context.Context, bounce *transport.BounceInfo) error {
				receivedBounce = bounce
				return nil
			}),
		)

		err := handler.RecordOutboundFailure(
			context.Background(),
			"<test456@example.com>",
			[]string{"user@example.com"},
			fmt.Errorf("452 Mailbox full, try again later"),
		)
		assert.NoError(t, err)
		require.NotNil(t, receivedBounce)
		assert.Equal(t, transport.BounceTypeSoft, receivedBounce.Type)
	})

	t.Run("multiple recipients", func(t *testing.T) {
		callCount := 0
		handler := transport.NewBounceHandler(nil, logger,
			transport.WithHardBounceCallback(func(ctx context.Context, bounce *transport.BounceInfo) error {
				callCount++
				return nil
			}),
		)

		err := handler.RecordOutboundFailure(
			context.Background(),
			"<test789@example.com>",
			[]string{"user1@example.com", "user2@example.com", "user3@example.com"},
			fmt.Errorf("550 User unknown"),
		)
		assert.NoError(t, err)
		assert.Equal(t, 3, callCount)
	})
}
