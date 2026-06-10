package config

import (
	"encoding/json"
	"fmt"
	"time"
)

type Config struct {
	CREForwarderAddress           string        `json:"creForwarderAddress"`
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
