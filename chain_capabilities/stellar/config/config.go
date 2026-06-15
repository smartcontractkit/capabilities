package config

import (
	"time"
)

// Config is the Stellar capability configuration.
//
// Stellar consensus reads are the only functionality wired today, so write/forwarder related
// fields present in other chain capabilities are intentionally omitted.
type Config struct {
	// Network is the relayer network identifier, e.g. "stellar".
	Network string `json:"network"`
	// ChainID is the Stellar network id (network passphrase hash) used to resolve the chain selector.
	ChainID string `json:"chainId"`
	// IsLocal runs against a local network (local CRE runs only); chain selector resolution is skipped.
	IsLocal bool `json:"isLocal,omitempty"`

	// ObservationPollerWorkersCount is the number of concurrent observation pollers.
	ObservationPollerWorkersCount uint `json:"observationPollerWorkersCount"`
	// ObservationPollPeriod is how often a volatile request re-observes the chain.
	ObservationPollPeriod time.Duration `json:"observationPollPeriod"`
	// UnknownRequestsTTL is how long results for not-yet-tracked requests are cached.
	UnknownRequestsTTL time.Duration `json:"unknownRequestsTTL"`
}
