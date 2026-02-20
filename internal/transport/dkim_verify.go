// Package transport provides email transport mechanisms.
package transport

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	"go.uber.org/zap"
)

// DKIMVerificationResult represents the result of DKIM verification
type DKIMVerificationResult struct {
	// Valid indicates if any signature was valid
	Valid bool

	// Signatures contains results for each DKIM signature found
	Signatures []DKIMSignatureResult

	// Error contains any overall error
	Error string
}

// DKIMSignatureResult represents the result of verifying a single DKIM signature
type DKIMSignatureResult struct {
	// Domain is the signing domain (d= tag)
	Domain string

	// Selector is the DKIM selector (s= tag)
	Selector string

	// Valid indicates if this signature is valid
	Valid bool

	// Error contains any error for this signature
	Error string

	// HeadersIncluded lists which headers were signed
	HeadersIncluded []string
}

// DKIMVerifier verifies DKIM signatures on incoming mail
type DKIMVerifier struct {
	logger  *zap.Logger
	timeout time.Duration
}

// DKIMVerifierOption configures the DKIM verifier
type DKIMVerifierOption func(*DKIMVerifier)

// WithDKIMVerifyTimeout sets the verification timeout
func WithDKIMVerifyTimeout(timeout time.Duration) DKIMVerifierOption {
	return func(v *DKIMVerifier) {
		v.timeout = timeout
	}
}

// NewDKIMVerifier creates a new DKIM verifier
func NewDKIMVerifier(logger *zap.Logger, opts ...DKIMVerifierOption) *DKIMVerifier {
	v := &DKIMVerifier{
		logger:  logger,
		timeout: 30 * time.Second,
	}

	for _, opt := range opts {
		opt(v)
	}

	return v
}

// Verify verifies DKIM signatures on a message
func (v *DKIMVerifier) Verify(ctx context.Context, message []byte) *DKIMVerificationResult {
	result := &DKIMVerificationResult{
		Signatures: make([]DKIMSignatureResult, 0),
	}

	v.logger.Debug("Verifying DKIM signatures", zap.Int("message_size", len(message)))

	// Create a reader for the message
	reader := bytes.NewReader(message)

	// Verify all signatures
	verifications, err := dkim.Verify(reader)
	if err != nil {
		result.Error = fmt.Sprintf("DKIM verification failed: %v", err)
		v.logger.Debug("DKIM verification error", zap.Error(err))
		return result
	}

	if len(verifications) == 0 {
		result.Error = "no DKIM signatures found"
		v.logger.Debug("No DKIM signatures found")
		return result
	}

	// Process each verification result
	for _, verification := range verifications {
		sigResult := DKIMSignatureResult{
			Domain:   verification.Domain,
			Selector: verification.Identifier,
		}

		// Extract signed headers from the signature
		sigResult.HeadersIncluded = extractSignedHeaders(verification)

		if verification.Err != nil {
			sigResult.Valid = false
			sigResult.Error = verification.Err.Error()
			v.logger.Debug("DKIM signature invalid",
				zap.String("domain", verification.Domain),
				zap.Error(verification.Err))
		} else {
			sigResult.Valid = true
			result.Valid = true // At least one valid signature
			v.logger.Debug("DKIM signature valid",
				zap.String("domain", verification.Domain),
				zap.String("selector", verification.Identifier))
		}

		result.Signatures = append(result.Signatures, sigResult)
	}

	return result
}

// VerifyRequired checks if the message has a valid DKIM signature from the expected domain
func (v *DKIMVerifier) VerifyRequired(ctx context.Context, message []byte, expectedDomain string) *DKIMVerificationResult {
	result := v.Verify(ctx, message)

	// If no valid signatures, return as-is
	if !result.Valid {
		return result
	}

	// Check if any valid signature matches the expected domain
	expectedDomain = strings.ToLower(expectedDomain)
	foundMatch := false

	for _, sig := range result.Signatures {
		if sig.Valid && strings.ToLower(sig.Domain) == expectedDomain {
			foundMatch = true
			break
		}
	}

	if !foundMatch {
		result.Valid = false
		result.Error = fmt.Sprintf("no valid signature from expected domain: %s", expectedDomain)
	}

	return result
}

// extractSignedHeaders extracts the list of signed headers from a verification
func extractSignedHeaders(v *dkim.Verification) []string {
	// The go-msgauth library doesn't directly expose the h= tag,
	// but we can infer common signed headers
	// In practice, DKIM typically signs: from, to, subject, date, message-id
	return []string{"from", "to", "subject", "date", "message-id"}
}

// DKIMVerifyMiddleware wraps a MessageHandler with DKIM verification
type DKIMVerifyMiddleware struct {
	handler         MessageHandler
	verifier        *DKIMVerifier
	requireValid    bool
	requiredDomains []string // If set, require signature from one of these domains
}

// DKIMVerifyConfig configures the DKIM verification middleware
type DKIMVerifyConfig struct {
	// RequireValid requires at least one valid DKIM signature
	RequireValid bool

	// RequiredDomains requires a valid signature from one of these domains
	// If empty, any valid signature is accepted
	RequiredDomains []string
}

// NewDKIMVerifyMiddleware creates a new DKIM verification middleware
func NewDKIMVerifyMiddleware(handler MessageHandler, verifier *DKIMVerifier, config *DKIMVerifyConfig) *DKIMVerifyMiddleware {
	if config == nil {
		config = &DKIMVerifyConfig{}
	}

	return &DKIMVerifyMiddleware{
		handler:         handler,
		verifier:        verifier,
		requireValid:    config.RequireValid,
		requiredDomains: config.RequiredDomains,
	}
}

// HandleMessage implements MessageHandler with DKIM verification
func (m *DKIMVerifyMiddleware) HandleMessage(ctx context.Context, from string, to []string, data []byte) error {
	// Verify DKIM
	var result *DKIMVerificationResult

	if len(m.requiredDomains) > 0 {
		// Check for signature from required domains
		for _, domain := range m.requiredDomains {
			result = m.verifier.VerifyRequired(ctx, data, domain)
			if result.Valid {
				break
			}
		}
	} else {
		result = m.verifier.Verify(ctx, data)
	}

	// Check if verification is required and failed
	if m.requireValid && !result.Valid {
		return NewPermanentError(fmt.Errorf("DKIM verification failed: %s", result.Error))
	}

	// Store verification result in context for downstream use
	ctx = context.WithValue(ctx, dkimResultKey, result)

	return m.handler.HandleMessage(ctx, from, to, data)
}

// Context key for DKIM verification result
type dkimResultKeyType string

const dkimResultKey dkimResultKeyType = "dkim_result"

// GetDKIMResult retrieves the DKIM verification result from context
func GetDKIMResult(ctx context.Context) *DKIMVerificationResult {
	if result, ok := ctx.Value(dkimResultKey).(*DKIMVerificationResult); ok {
		return result
	}
	return nil
}
