package config

import (
	"encoding/json"
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

func (c *Config) UnmarshalJSON(bs []byte) error {
	type config struct {
		Network string `json:"network"`
		ChainID string `json:"chainId"`
		IsLocal bool   `json:"isLocal,omitempty"`

		ReadsEnabled                  bool          `json:"readsEnabled"`
		ObservationPollerWorkersCount uint          `json:"observationPollerWorkersCount"`
		ObservationPollPeriod         time.Duration `json:"observationPollPeriod"`
		UnknownRequestsTTL            time.Duration `json:"unknownRequestsTTL"`
	}
	var cfg config
	if err := json.Unmarshal(bs, &cfg); err != nil {
		return err
	}

	c.Network = cfg.Network
	c.ChainID = cfg.ChainID
	c.IsLocal = cfg.IsLocal
	c.ObservationPollerWorkersCount = cfg.ObservationPollerWorkersCount
	c.ObservationPollPeriod = cfg.ObservationPollPeriod
	c.UnknownRequestsTTL = cfg.UnknownRequestsTTL

	return nil
}
