package trigger

import "github.com/smartcontractkit/chainlink-common/pkg/ratelimit"

const (
	defaultInitialIntervalMs = 100    // 100 milliseconds
	defaultDurationMs        = 30_000 // 30 seconds
	defaultMultiplier        = 2.0
	defaultGlobalRPS         = 100.0
	defaultGlobalBurst       = 100
	defaultPerSenderRPS      = 100.0
	defaultPerSenderBurst    = 100
)

type ServiceConfig struct {
	// AuthMetadataBatchSize is the number of auth metadata items to send in a single batch to the gateway.
	AuthMetdataBatchSize uint16 `json:"authMetadataBatchSize"`
	// SendChannelBufferSize is the size of the channel used to trigger workflows.
	SendChannelBufferSize uint16 `json:"sendChannelBufferSize"`
	// IncomingRateLimiter configuration for messages incoming to this node from the gateway.
	// The sender is a Gateway node, which is identified by the Gateway ID.
	IncomingRateLimiter ratelimit.RateLimiterConfig `json:"incomingRateLimiter" `
	// OutgoingRateLimiter is the configuration for outgoing messages from this node to the gateway.
	// The sender is a workflow owner
	OutgoingRateLimiter ratelimit.RateLimiterConfig `json:"outgoingRateLimiter"`
	// GatewayConfig defines the configuration for connecting to a gateway.
	GatewayConnectionConfig GatewayConnectionConfig `json:"gatewayConnection"`
}

type GatewayConnectionConfig struct {
	// InitialIntervalMs is the initial interval in milliseconds for the exponential backoff retry strategy when pushing auth metadata to the gateway.
	InitialIntervalMs uint32 `json:"initialIntervalMs"`
	// Multiplier is the multiplier for the exponential backoff retry strategy.
	Multiplier float64 `json:"multiplier"`
	// MaxPushAuthMetadataDurationMs is the maximum duration in milliseconds for broadcasting auth metadata to the gateway.
	MaxPushAuthMetadataDurationMs uint32 `json:"maxPushAuthMetadataDurationMs"`
	// MaxPullAuthMetadataDurationMs is the maximum duration in milliseconds for responding to pull auth metadata from the gateway.
	MaxPullAuthMetadataDurationMs uint32 `json:"maxPullAuthMetadataDurationMs"`
}

func applyDefaults(cfg ServiceConfig) ServiceConfig {
	cfg.GatewayConnectionConfig = gatewayConnectionConfigDefaults(cfg.GatewayConnectionConfig)
	cfg.OutgoingRateLimiter = outgoingRateLimiterConfigDefaults(cfg.OutgoingRateLimiter)
	cfg.IncomingRateLimiter = incomingRateLimiterConfigDefaults(cfg.IncomingRateLimiter)
	return cfg
}

func gatewayConnectionConfigDefaults(config GatewayConnectionConfig) GatewayConnectionConfig {
	if config.InitialIntervalMs == 0 {
		config.InitialIntervalMs = defaultInitialIntervalMs
	}
	if config.Multiplier == 0 {
		config.Multiplier = defaultMultiplier
	}
	if config.MaxPushAuthMetadataDurationMs == 0 {
		config.MaxPushAuthMetadataDurationMs = defaultDurationMs
	}
	if config.MaxPullAuthMetadataDurationMs == 0 {
		config.MaxPullAuthMetadataDurationMs = defaultDurationMs
	}
	return config
}

func incomingRateLimiterConfigDefaults(config ratelimit.RateLimiterConfig) ratelimit.RateLimiterConfig {
	if config.GlobalBurst == 0 {
		config.GlobalBurst = defaultGlobalBurst
	}
	if config.GlobalRPS == 0 {
		config.GlobalRPS = defaultGlobalRPS
	}
	if config.PerSenderBurst == 0 {
		config.PerSenderBurst = defaultPerSenderBurst
	}
	if config.PerSenderRPS == 0 {
		config.PerSenderRPS = defaultPerSenderRPS
	}
	return config
}
func outgoingRateLimiterConfigDefaults(config ratelimit.RateLimiterConfig) ratelimit.RateLimiterConfig {
	if config.GlobalBurst == 0 {
		config.GlobalBurst = defaultGlobalBurst
	}
	if config.GlobalRPS == 0 {
		config.GlobalRPS = defaultGlobalRPS
	}
	if config.PerSenderBurst == 0 {
		config.PerSenderBurst = defaultPerSenderBurst
	}
	if config.PerSenderRPS == 0 {
		config.PerSenderRPS = defaultPerSenderRPS
	}
	return config
}
