package validate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	durationpb "google.golang.org/protobuf/types/known/durationpb"

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
			Url:     "https://example.com",
			Method:  "POST",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    []byte(`{"foo":"bar"}`),
			Timeout: durationpb.New(1000 * time.Millisecond),
		}
		out, err := validator.ValidatedRequest(ctx, input)
		require.NoError(t, err)
		require.Equal(t, "https://example.com", out.Url)
		require.Equal(t, "POST", out.Method)
		require.Equal(t, input.Headers, out.Headers)
		require.Equal(t, input.Body, out.Body)
		require.Equal(t, time.Duration(1000)*time.Millisecond, out.Timeout.AsDuration())
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
			Timeout: durationpb.New(cresettings.Default.PerWorkflow.HTTPAction.ConnectionTimeout.DefaultValue + time.Second)}
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
			Url:     "https://foo",
			Method:  "POST",
			Body:    largeBody,
			Timeout: durationpb.New(1000 * time.Millisecond),
		}
		_, err := validator.ValidatedRequest(ctx, input)
		require.Error(t, err)
		require.Contains(t, err.Error(), "RequestSizeLimit limited")
	})

	t.Run("invalid HTTP method", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{Url: "https://foo", Method: "INVALID", Timeout: durationpb.New(1000 * time.Millisecond)}
		_, err := validator.ValidatedRequest(ctx, input)
		require.ErrorContains(t, err, "invalid HTTP method")
	})

	t.Run("valid cache settings", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		cacheAge := cresettings.Default.PerWorkflow.HTTPAction.CacheAgeLimit.DefaultValue / 2
		input := &http.Request{
			Url:     "https://foo",
			Method:  "GET",
			Timeout: durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{
				Store:  true,
				MaxAge: durationpb.New(cacheAge),
			},
		}
		out, err := validator.ValidatedRequest(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, out.CacheSettings)
		require.True(t, out.CacheSettings.Store)
		require.Equal(t, cacheAge, out.CacheSettings.MaxAge.AsDuration())
	})

	t.Run("cache settings with Store=true but MaxAge=0 is valid", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{
			Url:     "https://foo",
			Method:  "GET",
			Timeout: durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{
				Store:  true,
				MaxAge: durationpb.New(0),
			},
		}
		out, err := validator.ValidatedRequest(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, out.CacheSettings)
		require.True(t, out.CacheSettings.Store)
		require.Equal(t, time.Duration(0), out.CacheSettings.MaxAge.AsDuration())
	})

	t.Run("cache settings with negative MaxAge fails", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{
			Url:     "https://foo",
			Method:  "GET",
			Timeout: durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{
				Store:  false,
				MaxAge: durationpb.New(-1 * time.Second),
			},
		}
		_, err := validator.ValidatedRequest(ctx, input)
		require.ErrorContains(t, err, "MaxAge cannot be negative")
	})

	t.Run("nil cache settings is valid", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)
		input := &http.Request{
			Url:           "https://foo",
			Method:        "GET",
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: nil,
		}
		out, err := validator.ValidatedRequest(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, out.CacheSettings) // Default empty cache settings are added
		require.False(t, out.CacheSettings.Store)
		require.Equal(t, time.Duration(0), out.CacheSettings.MaxAge.AsDuration())
	})

	t.Run("cache age exceeds limit", func(t *testing.T) {
		t.Parallel()
		validator := testValidator(t)

		exceedingAge := cresettings.Default.PerWorkflow.HTTPAction.CacheAgeLimit.DefaultValue + time.Second
		input := &http.Request{
			Url:     "https://foo",
			Method:  "GET",
			Timeout: durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{
				Store:  true,
				MaxAge: durationpb.New(exceedingAge),
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
