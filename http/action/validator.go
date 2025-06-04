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

var allowedMethods = map[string]bool{
	"GET":     true,
	"POST":    true,
	"PUT":     true,
	"DELETE":  true,
	"PATCH":   true,
	"HEAD":    true,
	"OPTIONS": true,
}

// ValidateAndApplyDefaults validates the HTTP request fields and applies default values where necessary.
func ValidateAndApplyDefaults(input *http.Inputs, cfg common.ServiceConfig) (*http.Inputs, error) {
	// Validate and set defaults for request fields
	url := strings.TrimSpace(input.Url)
	if url == "" {
		return nil, fmt.Errorf("URL must not be empty")
	}

	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		method = "GET"
	}
	// Optionally, validate allowed HTTP methods
	if !allowedMethods[method] {
		return nil, fmt.Errorf("unsupported HTTP method: %s", method)
	}
	err := validateInputMaxLimits(input, cfg)
	if err != nil {
		return nil, fmt.Errorf("input validation failed: %w", err)
	}

	timeoutMs := input.TimeoutMs
	if timeoutMs == 0 {
		defaultTimeoutMs := getWithDefault(uint32(cfg.LimitsConfig.MaxTimeoutMs), defaultMaxTimeoutMs)
		if defaultTimeoutMs > math.MaxInt32 {
			return nil, fmt.Errorf("default timeout exceeds maximum allowed value: %d", math.MaxInt32)
		}
		timeoutMs = int32(defaultTimeoutMs)
	}

	return &http.Inputs{
		Url:       url,
		Method:    method,
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

func validateInputMaxLimits(input *http.Inputs, cfg common.ServiceConfig) error {
	maxTimeoutMs := getWithDefault(uint32(cfg.LimitsConfig.MaxTimeoutMs), defaultMaxTimeoutMs)
	maxHeaderCount := getWithDefault(uint32(cfg.LimitsConfig.MaxHeaderCount), defaultMaxHeaderCount)
	maxHeaderKeyLength := getWithDefault(uint32(cfg.LimitsConfig.MaxHeaderKeyLength), defaultMaxHeaderKeyLength)
	maxHeaderValueLength := getWithDefault(uint32(cfg.LimitsConfig.MaxHeaderValueLength), defaultMaxHeaderValueLength)
	maxBodyLength := getWithDefault(uint32(cfg.LimitsConfig.MaxBodyLength), defaultMaxBodyLength)

	if input.TimeoutMs < 0 || uint32(input.TimeoutMs) > maxTimeoutMs {
		return fmt.Errorf("timeout must be between 0 and %d milliseconds", maxTimeoutMs)
	}
	if len(input.Headers) > math.MaxUint32 {
		return fmt.Errorf("too many headers: exceeds uint32 limit")
	}
	if uint32(len(input.Headers)) > maxHeaderCount {
		return fmt.Errorf("too many headers: maximum allowed is %d", maxHeaderCount)
	}
	for k, v := range input.Headers {
		if len(k) > math.MaxUint32 {
			return fmt.Errorf("header key too long: %q (max %d)", k, math.MaxUint32)
		}
		if len(v) > math.MaxUint32 {
			return fmt.Errorf("header value for %q too long (max %d)", k, math.MaxUint32)
		}
		if uint32(len(k)) > maxHeaderKeyLength {
			return fmt.Errorf("header key too long: %q (max %d)", k, maxHeaderKeyLength)
		}
		if uint32(len(v)) > maxHeaderValueLength {
			return fmt.Errorf("header value for %q too long (max %d)", k, maxHeaderValueLength)
		}
	}
	if len(input.Body) > math.MaxUint32 {
		return fmt.Errorf("body too large: exceeds uint32 limit")
	}
	if uint32(len(input.Body)) > maxBodyLength {
		return fmt.Errorf("body too large: maximum allowed is %d bytes", maxBodyLength)
	}
	return nil
}
