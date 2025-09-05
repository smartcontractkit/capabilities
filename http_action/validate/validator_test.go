package validate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

func testValidator(t *testing.T) *Validator {
	lggr := logger.Test(t)
	limitsFactory := limits.Factory{
		Logger: lggr,
	}

	validator, err := NewValidator(lggr, limitsFactory)
	require.NoError(t, err)
	return validator
}

func TestValidatorCreation(t *testing.T) {
	t.Parallel()

	t.Run("creates validator successfully", func(t *testing.T) {
		validator := testValidator(t)
		require.NotNil(t, validator)
	})
}

func TestValidatedRequest(t *testing.T) {
	t.Parallel()
	ctx := contexts.WithCRE(context.Background(), contexts.CRE{Owner: "test-owner", Workflow: "test-workflow"})

	t.Run("valid input", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{
			Url:       "https://example.com",
			Method:    "POST",
			Headers:   map[string]string{"Content-Type": "application/json"},
			Body:      []byte(`{"foo":"bar"}`),
			TimeoutMs: 1000,
		}
		out, err := validator.ValidatedRequest(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "https://example.com", out.Url)
		require.Equal(t, "POST", out.Method)
		require.Equal(t, input.Headers, out.Headers)
		require.Equal(t, input.Body, out.Body)
		require.Equal(t, int32(1000), out.TimeoutMs)
	})

	t.Run("empty URL", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{Url: ""}
		_, err := validator.ValidatedRequest(ctx, input)
		require.ErrorContains(t, err, "URL must not be empty")
	})

	t.Run("timeout exceeds limit", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{Url: "https://foo", Method: "GET",
			TimeoutMs: int32(cresettings.Default.PerWorkflow.HTTPAction.ConnectionTimeout.DefaultValue.Milliseconds() + 1)} //nolint:gosec // G115
		_, err := validator.ValidatedRequest(ctx, input)
		require.Error(t, err)
		require.Contains(t, err.Error(), "ConnectionTimeout limited")
	})

	t.Run("request size exceeds limit", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)

		exceedingSize := cresettings.Default.PerWorkflow.HTTPAction.RequestSizeLimit.DefaultValue + 1000
		largeBody := make([]byte, exceedingSize)
		input := &http.Request{
			Url:       "https://foo",
			Method:    "POST",
			Body:      largeBody,
			TimeoutMs: 1000,
		}
		_, err := validator.ValidatedRequest(ctx, input)
		require.Error(t, err)
		require.Contains(t, err.Error(), "RequestSizeLimit limited")
	})

	t.Run("invalid HTTP method", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{Url: "https://foo", Method: "INVALID", TimeoutMs: 1000}
		_, err := validator.ValidatedRequest(ctx, input)
		require.ErrorContains(t, err, "invalid HTTP method")
	})

	t.Run("valid cache settings", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{
			Url:       "https://foo",
			Method:    "GET",
			TimeoutMs: 5000,
			CacheSettings: &http.CacheSettings{
				ReadFromCache: true,
				MaxAgeMs:      30000, // 30 seconds
			},
		}
		out, err := validator.ValidatedRequest(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, out.CacheSettings)
		require.True(t, out.CacheSettings.ReadFromCache)
		require.Equal(t, int32(30000), out.CacheSettings.MaxAgeMs)
	})

	t.Run("cache settings with ReadFromCache=true but MaxAgeMs=0 fails", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{
			Url:       "https://foo",
			Method:    "GET",
			TimeoutMs: 5000, // 5 seconds, well under the 10s limit
			CacheSettings: &http.CacheSettings{
				ReadFromCache: true,
				MaxAgeMs:      0,
			},
		}
		_, err := validator.ValidatedRequest(ctx, input)
		require.ErrorContains(t, err, "MaxAgeMs must be non-zero when ReadFromCache is true")
	})

	t.Run("cache settings with negative MaxAgeMs fails", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{
			Url:       "https://foo",
			Method:    "GET",
			TimeoutMs: 5000, // 5 seconds, well under the 10s limit
			CacheSettings: &http.CacheSettings{
				ReadFromCache: false,
				MaxAgeMs:      -1,
			},
		}
		_, err := validator.ValidatedRequest(ctx, input)
		require.ErrorContains(t, err, "MaxAgeMs cannot be negative")
	})

	t.Run("nil cache settings is valid", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{
			Url:           "https://foo",
			Method:        "GET",
			TimeoutMs:     5000,
			CacheSettings: nil,
		}
		out, err := validator.ValidatedRequest(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, out.CacheSettings) // Default empty cache settings are added
	})

	t.Run("cache age exceeds limit", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)

		exceedingAgeMs := int32(cresettings.Default.PerWorkflow.HTTPAction.CacheAgeLimit.DefaultValue.Milliseconds() + 1000) //nolint:gosec
		input := &http.Request{
			Url:       "https://foo",
			Method:    "GET",
			TimeoutMs: 5000,
			CacheSettings: &http.CacheSettings{
				ReadFromCache: true,
				MaxAgeMs:      exceedingAgeMs,
			},
		}
		_, err := validator.ValidatedRequest(ctx, input)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cache age validation failed")
		require.Contains(t, err.Error(), "CacheAgeLimit limited")
	})
}

func TestValidateResponseSize(t *testing.T) {
	t.Parallel()
	ctx := contexts.WithCRE(context.Background(), contexts.CRE{Owner: "test-owner", Workflow: "test-workflow"})

	t.Run("valid response size", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		response := []byte("small response")
		err := validator.ValidateResponseSize(ctx, response)
		require.NoError(t, err)
	})

	t.Run("response size exceeds limit", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)

		exceedingSize := cresettings.Default.PerWorkflow.HTTPAction.ResponseSizeLimit.DefaultValue + 1000
		largeResponse := make([]byte, exceedingSize)

		err := validator.ValidateResponseSize(ctx, largeResponse)
		require.Error(t, err)
		require.Contains(t, err.Error(), "ResponseSizeLimit limited")
	})
}
