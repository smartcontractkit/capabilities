package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

var allowedMethods = map[string]struct{}{
	"GET":    {},
	"POST":   {},
	"PUT":    {},
	"DELETE": {},
	"PATCH":  {},
}

const defaultTimeoutMs = 30_000

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
	url := strings.TrimSpace(input.Url)
	if url == "" {
		return nil, fmt.Errorf("URL must not be empty")
	}
	if input.TimeoutMs == 0 {
		input.TimeoutMs = defaultTimeoutMs
	}

	err := v.validateInputWithLimiters(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("input validation failed: %w", err)
	}

	err = v.validateCacheSettings(ctx, input.CacheSettings)
	if err != nil {
		return nil, fmt.Errorf("cache settings validation failed: %w", err)
	}

	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if _, ok := allowedMethods[method]; !ok {
		return nil, fmt.Errorf("invalid HTTP method: %s", method)
	}

	input.Method = method

	req := &http.Request{
		Url:       url,
		Method:    input.Method,
		Headers:   input.Headers,
		Body:      input.Body,
		TimeoutMs: input.TimeoutMs,
	}

	if input.CacheSettings != nil {
		req.CacheSettings = &http.CacheSettings{
			ReadFromCache: input.CacheSettings.ReadFromCache,
			MaxAgeMs:      input.CacheSettings.MaxAgeMs,
		}
	} else {
		req.CacheSettings = &http.CacheSettings{} // Default to empty cache settings if not provided
	}

	return req, nil
}

// validateInputWithLimiters validates input using bound limiters instead of config limits
func (v *Validator) validateInputWithLimiters(ctx context.Context, input *http.Request) error {
	marshaled, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("failed to marshal request for size calculation: %w", err)
	}

	requestSize := config.Size(len(marshaled))
	if err := v.requestSizeLimiter.Check(ctx, requestSize); err != nil {
		return err
	}

	// Check connection timeout limit
	timeout := time.Duration(input.TimeoutMs) * time.Millisecond
	if err := v.connectionTimeoutLimiter.Check(ctx, timeout); err != nil {
		return err
	}
	return nil
}

// validateCacheSettings validates cache settings using the cache age limiter
func (v *Validator) validateCacheSettings(ctx context.Context, cacheSettings *http.CacheSettings) error {
	if cacheSettings == nil {
		return nil
	}

	if cacheSettings.MaxAgeMs < 0 {
		return fmt.Errorf("MaxAgeMs cannot be negative")
	}

	cacheAge := time.Duration(cacheSettings.MaxAgeMs) * time.Millisecond
	if err := v.cacheAgeLimiter.Check(ctx, cacheAge); err != nil {
		return fmt.Errorf("cache age validation failed: %w", err)
	}

	if cacheSettings.ReadFromCache && cacheSettings.MaxAgeMs == 0 {
		return fmt.Errorf("MaxAgeMs must be non-zero when ReadFromCache is true")
	}

	return nil
}

// ValidateResponseSize checks if the response size is within limits
func (v *Validator) ValidateResponseSize(ctx context.Context, response []byte) error {
	responseSize := config.Size(len(response))
	return v.responseSizeLimiter.Check(ctx, responseSize)
}
