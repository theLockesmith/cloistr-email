package transport

import (
	"testing"
	"time"
)

// TestWithDefaults_PartialLiteralGetsSafeLimits reproduces the production
// outage.
//
// cmd/email/main.go builds SMTPServerConfig as a struct literal setting five
// fields and leaving the rest at Go's zero value. NewSMTPServer used to apply
// DefaultSMTPServerConfig only when config was nil, so a non-nil partial literal
// silently kept every zero:
//
//	MaxMessageSize 0 -> maxSize 0 -> `if n > maxSize` rejects EVERY message with
//	                    552 5.3.4 "Message too large".
//
// Inbound mail had therefore never worked. A one-line reply from Gmail bounced
// exactly as a 30MB attachment would, which is why the symptom read as a size
// problem rather than a total outage.
func TestWithDefaults_PartialLiteralGetsSafeLimits(t *testing.T) {
	// Exactly the shape main.go passes.
	got := withDefaults(&SMTPServerConfig{
		ListenAddr:     ":25",
		Domain:         "mail.cloistr.xyz",
		AllowedDomains: []string{"cloistr.xyz"},
		TLSCertFile:    "/tls/tls.crt",
		TLSKeyFile:     "/tls/tls.key",
	})

	if got.MaxMessageSize <= 0 {
		t.Errorf("MaxMessageSize = %d; a zero limit rejects EVERY inbound message "+
			"with 552 Message too large", got.MaxMessageSize)
	}
	if got.MaxRecipients <= 0 {
		t.Errorf("MaxRecipients = %d, want a positive limit", got.MaxRecipients)
	}
	// Zero timeouts mean NO deadline: a stalled peer holds the connection and its
	// goroutine indefinitely.
	if got.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout = %v; zero means no read deadline at all", got.ReadTimeout)
	}
	if got.WriteTimeout <= 0 {
		t.Errorf("WriteTimeout = %v; zero means no write deadline at all", got.WriteTimeout)
	}
}

// Explicit values must survive — defaults fill gaps, they do not override.
func TestWithDefaults_DoesNotOverrideExplicitValues(t *testing.T) {
	got := withDefaults(&SMTPServerConfig{
		ListenAddr:     ":2525",
		Domain:         "custom.example",
		MaxMessageSize: 1024,
		MaxRecipients:  7,
		ReadTimeout:    3 * time.Second,
		WriteTimeout:   4 * time.Second,
	})

	if got.ListenAddr != ":2525" || got.Domain != "custom.example" {
		t.Errorf("addr/domain overridden: %s %s", got.ListenAddr, got.Domain)
	}
	if got.MaxMessageSize != 1024 || got.MaxRecipients != 7 {
		t.Errorf("limits overridden: size=%d recipients=%d", got.MaxMessageSize, got.MaxRecipients)
	}
	if got.ReadTimeout != 3*time.Second || got.WriteTimeout != 4*time.Second {
		t.Errorf("timeouts overridden: %v %v", got.ReadTimeout, got.WriteTimeout)
	}
}

// The caller's struct must not be rewritten by a constructor.
func TestWithDefaults_DoesNotMutateCaller(t *testing.T) {
	in := &SMTPServerConfig{Domain: "mail.cloistr.xyz"}
	_ = withDefaults(in)
	if in.MaxMessageSize != 0 || in.ReadTimeout != 0 {
		t.Errorf("caller's config was mutated: size=%d read=%v", in.MaxMessageSize, in.ReadTimeout)
	}
}

func TestWithDefaults_NilReturnsFullDefaults(t *testing.T) {
	got := withDefaults(nil)
	if got.MaxMessageSize != 25*1024*1024 || got.MaxRecipients != 100 {
		t.Errorf("nil did not yield full defaults: %+v", got)
	}
}
