package common

import "github.com/smartcontractkit/chainlink-common/pkg/ratelimit"

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
	ProxyMode string `json:"proxyMode"`
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
