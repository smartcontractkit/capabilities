package capability

import (
	commonconfig "github.com/smartcontractkit/chainlink-common/pkg/config"
)

// PyroscopeConfig configures continuous profiling. An empty ServerAddress leaves profiling off.
type PyroscopeConfig struct {
	// ServerAddress is the pyroscope server address. Profiling is disabled when unset.
	ServerAddress string
	// AuthToken is the pyroscope auth token.
	AuthToken commonconfig.SecretString
	// Environment is the environment tag attached to profiles.
	Environment string
	// LinkTracesToProfiles tags spans with the profile they were recorded in. Needs tracing enabled.
	LinkTracesToProfiles bool
	// PPROFBlockProfileRate is runtime.SetBlockProfileRate's rate. Zero leaves block profiling off.
	PPROFBlockProfileRate int
	// PPROFMutexProfileFraction is runtime.SetMutexProfileFraction's rate. Zero leaves mutex
	// profiling off.
	PPROFMutexProfileFraction int
}
