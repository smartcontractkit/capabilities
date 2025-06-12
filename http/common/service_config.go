package common

import "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"

// ServiceConfig defines the configuration for the HTTP Actions service.
// TODO: move this out of common
type ServiceConfig struct {
	// RateLimiter configuration for messages incoming to this node from the gateway.
	// The sender is a Gateway node, which is identified by the Gateway ID.
	RateLimiter gateway.RateLimiterConfig `toml:"incomingRateLimiter" json:"incomingRateLimiter" yaml:"incomingRateLimiter" mapstructure:"incomingRateLimiter"`
	// OutgoingRateLimiter is the configuration for outgoing messages from this node to the gateway.
	// The sender is a workflow owner
	OutgoingRateLimiter gateway.RateLimiterConfig `toml:"outgoingRateLimiter" json:"outgoingRateLimiter" yaml:"outgoingRateLimiter" mapstructure:"outgoingRateLimiter"`
	// LimitsConfig groups HTTP-related configuration fields.
	LimitsConfig LimitsConfig `toml:"limits" json:"limits" yaml:"limits" mapstructure:"limits"`
	// ProxyMode is the mode of the outbound proxy. can be "gateway", "direct"
	ProxyMode string `toml:"proxyMode" json:"proxyMode" yaml:"proxyMode" mapstructure:"proxyMode"`
	// GatewayConnectionConfig defines the configuration for connecting to a gateway.
	GatewayConnectionConfig GatewayConnectionConfig `toml:"gatewayConnection" json:"gatewayConnection" yaml:"gatewayConnection" mapstructure:"gatewayConnection"`
}

// GatewayConnectionConfig defines the configuration for connecting to a gateway.
type GatewayConnectionConfig struct {
	// InitialIntervalMs is the initial interval in milliseconds for the exponential backoff retry strategy.
	InitialIntervalMs uint32 `toml:"initialIntervalMs" json:"initialIntervalMs" yaml:"initialIntervalMs" mapstructure:"initialIntervalMs"`
	// MaxElapsedTimeMs is the maximum elapsed time in milliseconds for the exponential backoff retry strategy.
	MaxElapsedTimeMs uint32 `toml:"maxElapsedTimeMs" json:"maxElapsedTimeMs" yaml:"maxElapsedTimeMs" mapstructure:"maxElapsedTimeMs"`
	// Multiplier is the multiplier for the exponential backoff retry strategy.
	Multiplier float64 `toml:"multiplier" json:"multiplier" yaml:"multiplier" mapstructure:"multiplier"`
}

// LimitsConfig groups HTTP-related configuration fields for both requests and responses.
type LimitsConfig struct {
	// MaxTimeoutMs is the timeout for HTTP requests in milliseconds.
	MaxTimeoutMs uint32 `toml:"timeoutMs" json:"timeoutMs" yaml:"timeoutMs" mapstructure:"timeoutMs"`
	// MaxResponseBytes is the maximum number of bytes to read from the response body.
	MaxResponseBytes uint32 `toml:"maxResponseBytes" json:"maxResponseBytes" yaml:"maxResponseBytes" mapstructure:"maxResponseBytes"`
	// MaxHeaderCount is the maximum number of headers allowed in a request.
	MaxHeaderCount uint32 `toml:"headerCount" json:"headerCount" yaml:"headerCount" mapstructure:"headerCount"`
	// MaxHeaderKeyLength is the maximum length of a header key.
	MaxHeaderKeyLength uint32 `toml:"maxHeaderKeyLength" json:"maxHeaderKeyLength" yaml:"maxHeaderKeyLength" mapstructure:"maxHeaderKeyLength"`
	// MaxHeaderValueLength is the maximum length of a header value.
	MaxHeaderValueLength uint32 `toml:"maxHeaderValueLength" json:"maxHeaderValueLength" yaml:"maxHeaderValueLength" mapstructure:"maxHeaderValueLength"`
	// MaxBodyLength is the maximum length of the request body.
	MaxBodyLength uint32 `toml:"maxBodyLength" json:"maxBodyLength" yaml:"maxBodyLength" mapstructure:"maxBodyLength"`
}
