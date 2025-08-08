package action

import (
	"fmt"
	"math"
	"strings"

	"github.com/smartcontractkit/capabilities/http_action/common"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
)

const (
	defaultMaxTimeoutMs         = 20_000
	defaultMaxHeaderCount       = 50
	defaultMaxHeaderKeyLength   = 256
	defaultMaxHeaderValueLength = 1024
	defaultMaxBodyLength        = 10 * 1024 * 1024 // 1 MB
	defaultGlobalRPS            = 100.0
	defaultGlobalBurst          = 100
	defaultPerSenderRPS         = 100.0
	defaultPerSenderBurst       = 100
	defaultWorkflowOwnerRPS     = 5.0
	defaultWorkflowOwnerBurst   = 50
)

var allowedMethods = map[string]struct{}{
	"GET":    {},
	"POST":   {},
	"PUT":    {},
	"DELETE": {},
	"PATCH":  {},
}

// ApplyDefaultsAndValidate validates and applies default values to the service configuration.
func ApplyDefaultsAndValidate(cfg *common.ServiceConfig) (*common.ServiceConfig, error) {
	maxTimeoutMs := getWithDefault(cfg.LimitsConfig.MaxTimeoutMs, defaultMaxTimeoutMs)
	maxHeaderCount := getWithDefault(cfg.LimitsConfig.MaxHeaderCount, defaultMaxHeaderCount)
	maxHeaderKeyLength := getWithDefault(cfg.LimitsConfig.MaxHeaderKeyLength, defaultMaxHeaderKeyLength)
	maxHeaderValueLength := getWithDefault(cfg.LimitsConfig.MaxHeaderValueLength, defaultMaxHeaderValueLength)
	maxRequestBytes := getWithDefault(cfg.LimitsConfig.MaxRequestBytes, defaultMaxBodyLength)
	maxResponseBytes := getWithDefault(cfg.LimitsConfig.MaxResponseBytes, defaultMaxBodyLength)

	if cfg.LimitsConfig.MaxTimeoutMs > math.MaxInt32 {
		return nil, fmt.Errorf("MaxTimeoutMs exceeds int32 maximum: %d", math.MaxInt32)
	}
	limitsConfig := common.LimitsConfig{
		MaxTimeoutMs:         maxTimeoutMs,
		MaxHeaderCount:       maxHeaderCount,
		MaxHeaderKeyLength:   maxHeaderKeyLength,
		MaxHeaderValueLength: maxHeaderValueLength,
		MaxRequestBytes:      maxRequestBytes,
		MaxResponseBytes:     maxResponseBytes,
	}
	cfg.LimitsConfig = limitsConfig
	cfg.OutgoingRateLimiter = ratelimit.RateLimiterConfig{
		GlobalRPS:      getWithDefault(cfg.OutgoingRateLimiter.GlobalRPS, defaultGlobalRPS),
		GlobalBurst:    getWithDefault(cfg.OutgoingRateLimiter.GlobalBurst, defaultGlobalBurst),
		PerSenderRPS:   getWithDefault(cfg.OutgoingRateLimiter.PerSenderRPS, defaultWorkflowOwnerRPS),
		PerSenderBurst: getWithDefault(cfg.OutgoingRateLimiter.PerSenderBurst, defaultWorkflowOwnerBurst),
	}
	cfg.IncomingRateLimiter = ratelimit.RateLimiterConfig{
		GlobalRPS:      getWithDefault(cfg.IncomingRateLimiter.GlobalRPS, defaultGlobalRPS),
		GlobalBurst:    getWithDefault(cfg.IncomingRateLimiter.GlobalBurst, defaultGlobalBurst),
		PerSenderRPS:   getWithDefault(cfg.IncomingRateLimiter.PerSenderRPS, defaultPerSenderRPS),
		PerSenderBurst: getWithDefault(cfg.IncomingRateLimiter.PerSenderBurst, defaultPerSenderRPS),
	}
	return cfg, nil
}

// ValidatedRequest validates the HTTP request fields and applies default values where necessary.
func ValidatedRequest(input *http.Request, cfg common.ServiceConfig) (*http.Request, error) {
	// Validate and set defaults for request fields
	url := strings.TrimSpace(input.Url)
	if url == "" {
		return nil, fmt.Errorf("URL must not be empty")
	}

	err := validateInputMaxLimits(input, cfg)
	if err != nil {
		return nil, fmt.Errorf("input validation failed: %w", err)
	}

	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if _, ok := allowedMethods[method]; !ok {
		return nil, fmt.Errorf("invalid HTTP method: %s", method)
	}

	input.Method = method

	timeoutMs := input.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = int32(cfg.LimitsConfig.MaxTimeoutMs) //nolint:gosec // G115 (validated in ApplyDefaultsAndValidate)
	}

	req := &http.Request{
		Url:       url,
		Method:    input.Method,
		Headers:   input.Headers,
		Body:      input.Body,
		TimeoutMs: timeoutMs,
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

// getWithDefault returns the first non-zero (or non-default) value among the arguments for any comparable type
func getWithDefault[T comparable](cfgVal, defaultVal T) T {
	var zero T
	if cfgVal != zero {
		return cfgVal
	}
	return defaultVal
}

func validateInputMaxLimits(input *http.Request, cfg common.ServiceConfig) error {
	if input.TimeoutMs < 0 || uint32(input.TimeoutMs) > cfg.LimitsConfig.MaxTimeoutMs {
		return fmt.Errorf("timeout must be between 0 and %d milliseconds", cfg.LimitsConfig.MaxTimeoutMs)
	}
	if len(input.Headers) > math.MaxUint32 {
		return fmt.Errorf("too many headers: exceeds uint32 limit")
	}
	if uint32(len(input.Headers)) > cfg.LimitsConfig.MaxHeaderCount { // nolint:gosec // G115
		return fmt.Errorf("too many headers: maximum allowed is %d", cfg.LimitsConfig.MaxHeaderCount)
	}
	for k, v := range input.Headers {
		if len(k) > math.MaxUint32 {
			return fmt.Errorf("header key too long: %q (max %d)", k, math.MaxUint32)
		}
		if len(v) > math.MaxUint32 {
			return fmt.Errorf("header value for %q too long (max %d)", k, math.MaxUint32)
		}
		if uint32(len(k)) > cfg.LimitsConfig.MaxHeaderKeyLength { // nolint:gosec // G115
			return fmt.Errorf("header key too long: %q (max %d)", k, cfg.LimitsConfig.MaxHeaderCount)
		}
		if uint32(len(v)) > cfg.LimitsConfig.MaxHeaderValueLength { // nolint:gosec // G115
			return fmt.Errorf("header value for %q too long (max %d)", k, cfg.LimitsConfig.MaxHeaderValueLength)
		}
	}
	if len(input.Body) > math.MaxUint32 {
		return fmt.Errorf("body too large: exceeds uint32 limit")
	}
	if uint32(len(input.Body)) > cfg.LimitsConfig.MaxRequestBytes { // nolint:gosec // G115
		return fmt.Errorf("body too large: maximum allowed is %d bytes", cfg.LimitsConfig.MaxRequestBytes)
	}
	return nil
}
