package action

import (
	"fmt"
	"math"
	"strings"

	"github.com/smartcontractkit/capabilities/http/common"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
)

const defaultMaxTimeoutMs = 20_000
const defaultMaxHeaderCount = 50
const defaultMaxHeaderKeyLength = 256
const defaultMaxHeaderValueLength = 1024
const defaultMaxBodyLength = 10 * 1024 * 1024 // 1 MB

var allowedMethods = map[string]struct{}{
	"GET":    {},
	"POST":   {},
	"PUT":    {},
	"DELETE": {},
	"PATCH":  {},
}

func ValidatedServiceConfig(cfg *common.ServiceConfig) (*common.ServiceConfig, error) {
	maxTimeoutMs := getWithDefault(cfg.LimitsConfig.MaxTimeoutMs, defaultMaxTimeoutMs)
	maxHeaderCount := getWithDefault(cfg.LimitsConfig.MaxHeaderCount, defaultMaxHeaderCount)
	maxHeaderKeyLength := getWithDefault(cfg.LimitsConfig.MaxHeaderKeyLength, defaultMaxHeaderKeyLength)
	maxHeaderValueLength := getWithDefault(cfg.LimitsConfig.MaxHeaderValueLength, defaultMaxHeaderValueLength)
	maxBodyLength := getWithDefault(cfg.LimitsConfig.MaxBodyLength, defaultMaxBodyLength)

	if cfg.LimitsConfig.MaxTimeoutMs > math.MaxInt32 {
		return nil, fmt.Errorf("MaxTimeoutMs exceeds int32 maximum: %d", math.MaxInt32)
	}
	limitsConfig := common.LimitsConfig{
		MaxTimeoutMs:         maxTimeoutMs,
		MaxHeaderCount:       maxHeaderCount,
		MaxHeaderKeyLength:   maxHeaderKeyLength,
		MaxHeaderValueLength: maxHeaderValueLength,
		MaxBodyLength:        maxBodyLength,
	}
	cfg.LimitsConfig = limitsConfig
	return cfg, nil
}

// ValidateAndApplyDefaults validates the HTTP request fields and applies default values where necessary.
func ValidateAndApplyDefaults(input *http.Request, cfg common.ServiceConfig) (*http.Request, error) {
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
		timeoutMs = int32(cfg.LimitsConfig.MaxTimeoutMs) //nolint:gosec // G115 (validated in action.go)
	}

	return &http.Request{
		Url:       url,
		Method:    input.Method,
		Headers:   input.Headers,
		Body:      input.Body,
		TimeoutMs: timeoutMs,
	}, nil
}

// getWithDefault returns the first non-zero value among the arguments, or the last one as a fallback.
func getWithDefault(cfgVal uint32, defaultVal uint32) uint32 {
	if cfgVal != 0 {
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
	if uint32(len(input.Headers)) > cfg.LimitsConfig.MaxHeaderCount {
		return fmt.Errorf("too many headers: maximum allowed is %d", cfg.LimitsConfig.MaxHeaderCount)
	}
	for k, v := range input.Headers {
		if len(k) > math.MaxUint32 {
			return fmt.Errorf("header key too long: %q (max %d)", k, math.MaxUint32)
		}
		if len(v) > math.MaxUint32 {
			return fmt.Errorf("header value for %q too long (max %d)", k, math.MaxUint32)
		}
		if uint32(len(k)) > cfg.LimitsConfig.MaxHeaderCount {
			return fmt.Errorf("header key too long: %q (max %d)", k, cfg.LimitsConfig.MaxHeaderCount)
		}
		if uint32(len(v)) > cfg.LimitsConfig.MaxHeaderValueLength {
			return fmt.Errorf("header value for %q too long (max %d)", k, cfg.LimitsConfig.MaxHeaderValueLength)
		}
	}
	if len(input.Body) > math.MaxUint32 {
		return fmt.Errorf("body too large: exceeds uint32 limit")
	}
	if uint32(len(input.Body)) > cfg.LimitsConfig.MaxResponseBytes {
		return fmt.Errorf("body too large: maximum allowed is %d bytes", cfg.LimitsConfig.MaxResponseBytes)
	}
	return nil
}
