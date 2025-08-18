package trigger

import "github.com/smartcontractkit/chainlink-common/pkg/ratelimit"

const (
	defaultInitialIntervalMs            = 100    // 100 milliseconds
	defaultDurationMs                   = 30_000 // 30 seconds
	defaultMultiplier                   = 2.0
	defaultGlobalRPS                    = 100.0
	defaultGlobalBurst                  = 100
	defaultPerSenderRPS                 = 100.0
	defaultPerSenderBurst               = 100
	defaultMetadataBatchSize            = 50
	defaultSendChannelBufferSize        = 1000
	defaultMaxAuthorizedKeysPerWorkflow = 100
	defaultRequestCacheTTL              = 24 * 60 * 60 // 24 hours in seconds
)

type ServiceConfig struct {
	// MetadataBatchSize is the number of metadata items to send in a single batch to the gateway.
	MetadataBatchSize uint16 `json:"metadataBatchSize"`
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
	// MaxAuthorizedKeysPerWorkflow is the maximum number of authorized keys per workflow.
	// This is used to limit the number of keys that can be registered per workflow.
	// This impacts the size of the auth metadata sent to the gateway.
	MaxAuthorizedKeysPerWorkflow uint16 `json:"maxAuthorizedKeysPerWorkflow"`
	// RequestCacheTTL is the time-to-live for cached request responses in milliseconds.
	// Used for idempotency - cached responses are returned for duplicate requests within this time window.
	RequestCacheTTL uint32 `json:"requestCacheTTL"`
}

type GatewayConnectionConfig struct {
	RetryConfig RetryConfig `json:"retryConfig"`
	// MaxPushMetadataDurationMs is the maximum duration in milliseconds for broadcasting metadata to the gateway.
	MaxPushMetadataDurationMs uint32 `json:"maxPushMetadataDurationMs"`
	// MaxPullMetadataDurationMs is the maximum duration in milliseconds for responding to pull metadata from the gateway.
	MaxPullMetadataDurationMs uint32 `json:"maxPullMetadataDurationMs"`
}

type RetryConfig struct {
	InitialIntervalMs int     `json:"initialIntervalMs"`
	MaxIntervalTimeMs int     `json:"maxIntervalTimeMs"`
	Multiplier        float64 `json:"multiplier"`
}

func applyDefaults(cfg ServiceConfig) ServiceConfig {
	cfg.GatewayConnectionConfig = gatewayConnectionConfigDefaults(cfg.GatewayConnectionConfig)
	cfg.OutgoingRateLimiter = outgoingRateLimiterConfigDefaults(cfg.OutgoingRateLimiter)
	cfg.IncomingRateLimiter = incomingRateLimiterConfigDefaults(cfg.IncomingRateLimiter)
	if cfg.MetadataBatchSize == 0 {
		cfg.MetadataBatchSize = defaultMetadataBatchSize
	}
	if cfg.SendChannelBufferSize == 0 {
		cfg.SendChannelBufferSize = defaultSendChannelBufferSize
	}
	if cfg.MaxAuthorizedKeysPerWorkflow == 0 {
		cfg.MaxAuthorizedKeysPerWorkflow = defaultMaxAuthorizedKeysPerWorkflow
	}
	if cfg.RequestCacheTTL == 0 {
		cfg.RequestCacheTTL = defaultRequestCacheTTL
	}
	return cfg
}

func gatewayConnectionConfigDefaults(config GatewayConnectionConfig) GatewayConnectionConfig {
	if config.RetryConfig.InitialIntervalMs == 0 {
		config.RetryConfig.InitialIntervalMs = defaultInitialIntervalMs
	}
	if config.RetryConfig.Multiplier == 0 {
		config.RetryConfig.Multiplier = defaultMultiplier
	}
	if config.RetryConfig.MaxIntervalTimeMs == 0 {
		config.RetryConfig.MaxIntervalTimeMs = defaultDurationMs
	}
	if config.MaxPushMetadataDurationMs == 0 {
		config.MaxPushMetadataDurationMs = defaultDurationMs
	}
	if config.MaxPullMetadataDurationMs == 0 {
		config.MaxPullMetadataDurationMs = defaultDurationMs
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
