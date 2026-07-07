package config

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
)

type Config struct {
	CREForwarderAddress string `json:"creForwarderAddress"`
	// ForwarderLookbackLedgers is how many ledgers back to search for ReportProcessed events (default 100).
	ForwarderLookbackLedgers      int64         `json:"forwarderLookbackLedgers"`
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
	if cfg.CREForwarderAddress == "" {
		return fmt.Errorf("creForwarderAddress is required")
	}
	if err := validateContractStrKey(cfg.CREForwarderAddress); err != nil {
		return fmt.Errorf("creForwarderAddress: %w", err)
	}
	*c = Config(cfg)
	return nil
}

func validateContractStrKey(address string) error {
	if _, err := strkey.Decode(strkey.VersionByteContract, address); err != nil {
		return fmt.Errorf("invalid contract address %q: %w", address, err)
	}
	return nil
}
