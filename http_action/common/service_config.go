package common

import "github.com/smartcontractkit/chainlink-common/pkg/ratelimit"

// ServiceConfig defines the configuration for the HTTP Actions service.
type ServiceConfig struct {
	// IncomingRateLimiter configuration for messages incoming to this node from the gateway.
	// The sender is a Gateway node, which is identified by the Gateway ID.
	IncomingRateLimiter ratelimit.RateLimiterConfig `json:"incomingRateLimiter"`
	// OutgoingRateLimiter is the configuration for outgoing messages from this node to the gateway.
	// The sender is a workflow owner
	OutgoingRateLimiter ratelimit.RateLimiterConfig `json:"outgoingRateLimiter"`
	// LimitsConfig groups HTTP-related configuration fields.
	LimitsConfig LimitsConfig `json:"limits"`
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

// LimitsConfig groups HTTP-related configuration fields for both requests and responses.
type LimitsConfig struct {
	// MaxTimeoutMs is the max timeout for HTTP requests in milliseconds.
	MaxTimeoutMs uint32 `json:"maxTimeoutMs"`
	// MaxResponseBytes is the maximum number of bytes to read from the response body.
	MaxResponseBytes uint32 `json:"maxResponseBytes"`
	// MaxHeaderCount is the maximum number of headers allowed in a request.
	MaxHeaderCount uint32 `json:"maxHeaderCount"`
	// MaxHeaderKeyLength is the maximum length of a header key.
	MaxHeaderKeyLength uint32 `json:"maxHeaderKeyLength"`
	// MaxHeaderValueLength is the maximum length of a header value.
	MaxHeaderValueLength uint32 `json:"maxHeaderValueLength"`
	// MaxRequestBytes is the maximum length of the request body.
	MaxRequestBytes uint32 `json:"maxRequestBytes"`
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
