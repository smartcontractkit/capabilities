package common

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

// WithDefaults is what a direct request may reach when nothing said otherwise:
// https, on its own port. Anything wider is a deployment's decision to make.
func (cfg HTTPClientConfig) WithDefaults() HTTPClientConfig {
	if len(cfg.AllowedPorts) == 0 {
		cfg.AllowedPorts = []int{443}
	}
	if len(cfg.AllowedSchemes) == 0 {
		cfg.AllowedSchemes = []string{"https"}
	}
	return cfg
}
