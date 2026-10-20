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
	ForwarderLookbackLedgers int64 `json:"forwarderLookbackLedgers"`
	// LedgerBoundsOffset makes a report tx valid for the next offset-1 ledgers (default 20; 4 is
	// the short validity, 5 without lag-compensated bounds in the relayer).
	LedgerBoundsOffset            uint32        `json:"ledgerBoundsOffset"`
	DeltaStage                    time.Duration `json:"deltaStage"`
	Network                       string        `json:"network"`
	ChainID                       string        `json:"chainId"`
	IsLocal                       bool          `json:"isLocal,omitempty"`
	ObservationPollerWorkersCount uint          `json:"observationPollerWorkersCount"`
	ObservationPollPeriod         time.Duration `json:"observationPollPeriod"`
	UnknownRequestsTTL            time.Duration `json:"unknownRequestsTTL"`
	MaxUnknownRequestsCacheSize   int           `json:"maxUnknownRequestsCacheSize"`
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
	if cfg.ForwarderLookbackLedgers < 0 {
		return fmt.Errorf("forwarderLookbackLedgers must be non-negative")
	}
	if cfg.LedgerBoundsOffset != 0 && cfg.LedgerBoundsOffset < 3 {
		return fmt.Errorf("ledgerBoundsOffset must be 0 (default) or at least 3: MaxLedger is exclusive, so %d leaves %d valid ledger(s), fewer than the two a resend needs", cfg.LedgerBoundsOffset, cfg.LedgerBoundsOffset-1)
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
