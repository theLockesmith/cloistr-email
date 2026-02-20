package unit

import (
	"context"
	"testing"
	"time"

	"git.coldforge.xyz/coldforge/cloistr-email/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSPFResultConstants(t *testing.T) {
	assert.Equal(t, transport.SPFResult("pass"), transport.SPFPass)
	assert.Equal(t, transport.SPFResult("fail"), transport.SPFFail)
	assert.Equal(t, transport.SPFResult("softfail"), transport.SPFSoftFail)
	assert.Equal(t, transport.SPFResult("neutral"), transport.SPFNeutral)
	assert.Equal(t, transport.SPFResult("none"), transport.SPFNone)
	assert.Equal(t, transport.SPFResult("temperror"), transport.SPFTempError)
	assert.Equal(t, transport.SPFResult("permerror"), transport.SPFPermError)
}

func TestNewSPFValidator(t *testing.T) {
	logger := zap.NewNop()

	t.Run("with default options", func(t *testing.T) {
		validator := transport.NewSPFValidator(logger)
		require.NotNil(t, validator)
	})

	t.Run("with custom options", func(t *testing.T) {
		validator := transport.NewSPFValidator(logger,
			transport.WithSPFLookupLimit(5),
			transport.WithSPFTimeout(5*time.Second),
		)
		require.NotNil(t, validator)
	})
}

func TestSPFCheckInvalidIP(t *testing.T) {
	logger := zap.NewNop()
	validator := transport.NewSPFValidator(logger)

	result := validator.Check(context.Background(), "invalid-ip", "example.com", "sender@example.com")

	assert.Equal(t, transport.SPFPermError, result.Result)
	assert.Contains(t, result.Explanation, "invalid client IP")
}

func TestSPFCheckResultStruct(t *testing.T) {
	result := &transport.SPFCheckResult{
		Result:      transport.SPFPass,
		Domain:      "example.com",
		Explanation: "matched ip4:192.168.1.0/24",
		Record:      "v=spf1 ip4:192.168.1.0/24 -all",
	}

	assert.Equal(t, transport.SPFPass, result.Result)
	assert.Equal(t, "example.com", result.Domain)
	assert.NotEmpty(t, result.Explanation)
	assert.NotEmpty(t, result.Record)
}

// Note: Full SPF validation tests require DNS lookups
// and are better suited for integration tests.
// These unit tests focus on the validator creation and error handling.
