package config

import (
	"encoding/json"
	"fmt"
	"time"
)

type Config struct {
	CREForwarderAddress string `json:"creForwarderAddress"`
	// NodeAddress is the G... StrKey of the node's Stellar signing account.
	// It is passed as the transmitter argument to the on-chain forwarder report() call
	// and must be registered as a valid forwarder in the KeystoneForwarder contract.
	NodeAddress                   string        `json:"nodeAddress"`
	DeltaStage                    time.Duration `json:"deltaStage"`
	Network                       string        `json:"network"`
	ChainID                       string        `json:"chainId"`
	IsLocal                       bool          `json:"isLocal,omitempty"`
	ObservationPollerWorkersCount uint          `json:"observationPollerWorkersCount"`
	ObservationPollPeriod         time.Duration `json:"observationPollPeriod"`
	UnknownRequestsTTL            time.Duration `json:"unknownRequestsTTL"`
}

func (c *Config) UnmarshalJSON(bs []byte) error {
	type config Config
	var cfg config
	if err := json.Unmarshal(bs, &cfg); err != nil {
		return err
	}
	if cfg.Network == "" {
		return fmt.Errorf("network is required")
	}
	if cfg.ChainID == "" {
		return fmt.Errorf("chainId is required")
	}
	*c = Config(cfg)
	return nil
}
