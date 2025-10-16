package validate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	durationpb "google.golang.org/protobuf/types/known/durationpb"
)

var allowedMethods = map[string]struct{}{
	"GET":    {},
	"POST":   {},
	"PUT":    {},
	"DELETE": {},
	"PATCH":  {},
}

const defaultTimeoutMs = 5_000
const internalError = "internal error"

// Validator handles validation of HTTP requests and responses with proper limiters
type Validator struct {
	lggr                     logger.SugaredLogger
	responseSizeLimiter      limits.BoundLimiter[config.Size]
	requestSizeLimiter       limits.BoundLimiter[config.Size]
	connectionTimeoutLimiter limits.BoundLimiter[time.Duration]
	cacheAgeLimiter          limits.BoundLimiter[time.Duration]
}

// NewValidator creates a new Validator with initialized limiters
func NewValidator(lggr logger.Logger, limitsFactory limits.Factory) (*Validator, error) {
	responseSizeLimiter, err := limits.MakeBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.HTTPAction.ResponseSizeLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to create response size limiter: %w", err)
	}

	requestSizeLimiter, err := limits.MakeBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.HTTPAction.RequestSizeLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to create request size limiter: %w", err)
	}

	connectionTimeoutLimiter, err := limits.MakeBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.HTTPAction.ConnectionTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection timeout limiter: %w", err)
	}

	cacheAgeLimiter, err := limits.MakeBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.HTTPAction.CacheAgeLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache age limiter: %w", err)
	}

	return &Validator{
		lggr:                     logger.Sugared(logger.Named(lggr, "Validator")),
		responseSizeLimiter:      responseSizeLimiter,
		requestSizeLimiter:       requestSizeLimiter,
		connectionTimeoutLimiter: connectionTimeoutLimiter,
		cacheAgeLimiter:          cacheAgeLimiter,
	}, nil
}

// ValidatedRequest validates the HTTP request fields and applies default values where necessary.
func (v *Validator) ValidatedRequest(ctx context.Context, input *http.Request) (*http.Request, error) {
	if input == nil {
		return nil, fmt.Errorf("input cannot be nil")
	}

	url := strings.TrimSpace(input.Url)
	if url == "" {
		return nil, fmt.Errorf("URL must not be empty")
	}
	if input.Timeout == nil || input.Timeout.AsDuration() == 0 {
		input.Timeout = durationpb.New(time.Duration(defaultTimeoutMs) * time.Millisecond)
	}

	err := v.validateInputWithLimiters(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("input validation failed: %w", err)
	}

	cacheSettings, err := v.validatedCacheSettings(ctx, input.CacheSettings)
	if err != nil {
		return nil, fmt.Errorf("cache settings validation failed: %w", err)
	}

	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		return nil, fmt.Errorf("method cannot be empty")
	}
	if _, ok := allowedMethods[method]; !ok {
		return nil, fmt.Errorf("invalid HTTP method: %s", method)
	}

	input.Method = method

	req := &http.Request{
		Url:           url,
		Method:        input.Method,
		Headers:       input.Headers,
		Body:          input.Body,
		Timeout:       input.Timeout,
		CacheSettings: cacheSettings,
	}

	return req, nil
}

// validateInputWithLimiters validates input using bound limiters instead of config limits
func (v *Validator) validateInputWithLimiters(ctx context.Context, input *http.Request) error {
	marshaled, err := json.Marshal(input)
	if err != nil {
		v.lggr.Errorf("failed to marshal request for size calculation: %v", err)
		return errors.New(internalError)
	}

	requestSize := config.Size(len(marshaled))
	if err := v.requestSizeLimiter.Check(ctx, requestSize); err != nil {
		return err
	}
	return v.connectionTimeoutLimiter.Check(ctx, input.Timeout.AsDuration())
}

// validateCacheSettings validates cache settings using the cache age limiter
func (v *Validator) validatedCacheSettings(ctx context.Context, cacheSettings *http.CacheSettings) (*http.CacheSettings, error) {
	if cacheSettings == nil {
		return &http.CacheSettings{
			Store:  false,
			MaxAge: durationpb.New(0),
		}, nil
	}

	if cacheSettings.MaxAge == nil {
		cacheSettings.MaxAge = durationpb.New(0)
	}

	if cacheSettings.MaxAge.AsDuration() < 0 {
		return nil, fmt.Errorf("MaxAge cannot be negative")
	}

	if err := v.cacheAgeLimiter.Check(ctx, cacheSettings.MaxAge.AsDuration()); err != nil {
		return nil, fmt.Errorf("cache age validation failed: %w", err)
	}

	return cacheSettings, nil
}

// ValidateResponseSize checks if the response size is within limits
func (v *Validator) ValidateResponseSize(ctx context.Context, response []byte) error {
	return v.responseSizeLimiter.Check(ctx, config.SizeOf(response))
}
