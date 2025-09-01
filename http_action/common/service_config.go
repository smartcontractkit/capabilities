package common

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
)

type ProxyMode int

const (
	ProxyModeDirect ProxyMode = iota
	ProxyModeGateway
)

func (p ProxyMode) String() string {
	switch p {
	case ProxyModeDirect:
		return "direct"
	case ProxyModeGateway:
		return "gateway"
	default:
		return "unknown"
	}
}

// ParseProxyMode parses a string into a ProxyMode
func ParseProxyMode(s string) (ProxyMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "direct":
		return ProxyModeDirect, nil
	case "gateway":
		return ProxyModeGateway, nil
	default:
		return 0, fmt.Errorf("invalid proxy mode: %q, must be either 'direct' or 'gateway'", s)
	}
}

const (
	defaultGlobalRPS      = 100.0
	defaultGlobalBurst    = 100
	defaultPerSenderRPS   = 100.0
	defaultPerSenderBurst = 100
)

// ServiceConfig defines the configuration for the HTTP Actions service.
type ServiceConfig struct {
	// IncomingRateLimiter configuration for messages incoming to this node from the gateway.
	// The sender is a Gateway node, which is identified by the Gateway ID.
	IncomingRateLimiter ratelimit.RateLimiterConfig `json:"incomingRateLimiter"`
	// ProxyMode is the mode of the outbound proxy. can be "gateway", "direct"
	ProxyMode ProxyMode `json:"proxyMode"`
	// GatewayConnectionConfig defines the configuration for connecting to a gateway.
	GatewayConnectionConfig GatewayConnectionConfig `json:"gatewayConnection"`
	// HTTPClientConfig defines the configuration for the HTTP client used in "direct" mode.
	HTTPClientConfig HTTPClientConfig `json:"httpClient"`
}

// GatewayConnectionConfig defines the configuration for connecting to a gateway.
type GatewayConnectionConfig struct {
	// InitialIntervalMs is the initial interval in milliseconds for the exponential backoff retry strategy.
	InitialIntervalMs uint32 `json:"initialIntervalMs"`
	// MaxElapsedTimeMs is the maximum elapsed time in milliseconds for the exponential backoff retry strategy.
	MaxElapsedTimeMs uint32 `json:"maxElapsedTimeMs"`
	// Multiplier is the multiplier for the exponential backoff retry strategy.
	Multiplier float64 `json:"multiplier"`
}

// HTTPClientConfig defines configuration options for the HTTP client used in "direct" mode.
type HTTPClientConfig struct {
	// BlockedIPs is a list of IP addresses that are not allowed to be accessed.
	BlockedIPs []string `json:"blockedIPs"`
	// BlockedIPsCIDR is a list of CIDR blocks that are not allowed to be accessed.
	BlockedIPsCIDR []string `json:"blockedIPsCIDR"`
	// AllowedPorts is a list of ports that are allowed for outgoing HTTP requests.
	AllowedPorts []int `json:"allowedPorts"`
	// AllowedSchemes is a list of URL schemes (e.g., "http", "https") that are allowed.
	AllowedSchemes []string `json:"allowedSchemes"`
	// AllowedIPs is a list of IP addresses that are explicitly allowed to be accessed.
	AllowedIPs []string `json:"allowedIPs"`
	// AllowedIPsCIDR is a list of CIDR blocks that are explicitly allowed to be accessed.
	AllowedIPsCIDR []string `json:"allowedIPsCIDR"`
}

// UnmarshalJSON implements custom JSON unmarshaling for ServiceConfig.
// The ProxyMode is parsed from string to ProxyMode.
func (cfg *ServiceConfig) UnmarshalJSON(data []byte) error {
	type tempServiceConfig struct {
		IncomingRateLimiter     ratelimit.RateLimiterConfig `json:"incomingRateLimiter"`
		ProxyMode               string                      `json:"proxyMode"`
		GatewayConnectionConfig GatewayConnectionConfig     `json:"gatewayConnection"`
		HTTPClientConfig        HTTPClientConfig            `json:"httpClient"`
	}

	var temp tempServiceConfig
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	proxyMode, err := ParseProxyMode(temp.ProxyMode)
	if err != nil {
		return fmt.Errorf("failed to parse proxyMode: %w", err)
	}

	cfg.IncomingRateLimiter = temp.IncomingRateLimiter
	cfg.ProxyMode = proxyMode
	cfg.GatewayConnectionConfig = temp.GatewayConnectionConfig
	cfg.HTTPClientConfig = temp.HTTPClientConfig

	return nil
}

// MarshalJSON implements custom JSON marshaling for ServiceConfig.
// The ProxyMode is serialized as string.
func (cfg ServiceConfig) MarshalJSON() ([]byte, error) {
	type tempServiceConfig struct {
		IncomingRateLimiter     ratelimit.RateLimiterConfig `json:"incomingRateLimiter"`
		ProxyMode               string                      `json:"proxyMode"`
		GatewayConnectionConfig GatewayConnectionConfig     `json:"gatewayConnection"`
		HTTPClientConfig        HTTPClientConfig            `json:"httpClient"`
	}

	temp := tempServiceConfig{
		IncomingRateLimiter:     cfg.IncomingRateLimiter,
		ProxyMode:               cfg.ProxyMode.String(),
		GatewayConnectionConfig: cfg.GatewayConnectionConfig,
		HTTPClientConfig:        cfg.HTTPClientConfig,
	}

	return json.Marshal(temp)
}

func (cfg *ServiceConfig) ApplyDefault() {
	cfg.IncomingRateLimiter = ratelimit.RateLimiterConfig{
		GlobalRPS:      getWithDefault(cfg.IncomingRateLimiter.GlobalRPS, defaultGlobalRPS),
		GlobalBurst:    getWithDefault(cfg.IncomingRateLimiter.GlobalBurst, defaultGlobalBurst),
		PerSenderRPS:   getWithDefault(cfg.IncomingRateLimiter.PerSenderRPS, defaultPerSenderRPS),
		PerSenderBurst: getWithDefault(cfg.IncomingRateLimiter.PerSenderBurst, defaultPerSenderBurst),
	}

	if len(cfg.HTTPClientConfig.AllowedPorts) == 0 {
		cfg.HTTPClientConfig.AllowedPorts = []int{80, 443}
	}
	if len(cfg.HTTPClientConfig.AllowedSchemes) == 0 {
		cfg.HTTPClientConfig.AllowedSchemes = []string{"http", "https"}
	}
}

func getWithDefault[T comparable](cfgVal, defaultVal T) T {
	var zero T
	if cfgVal != zero {
		return cfgVal
	}
	return defaultVal
}
