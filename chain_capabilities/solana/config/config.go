package config

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gagliardetto/solana-go"
)

type Config struct {
	CREForwarderAddress solana.PublicKey `json:"creForwarderAddress"` // Address of forwarder program
	CREForwarderState   solana.PublicKey `json:"creForwarderState"`   // Address of forwarder program state
	Transmitter         solana.PublicKey
	IsLocal             bool          `json:"isLocal,omitempty"` // Run against local validator (for local CRE runs only)
	Network             string        `json:"network"`
	ChainID             string        `json:"chainId"`
	DeltaStage          time.Duration `json:"deltaStage"` // Staggered transmission scheduling delay per queue position

	ReadsEnabled                  bool          `json:"readsEnabled"`
	ObservationPollerWorkersCount uint          `json:"observationPollerWorkersCount"`
	ObservationPollPeriod         time.Duration `json:"observationPollPeriod"`
	UnknownRequestsTTL            time.Duration `json:"unknownRequestsTTL"`
}

func (c *Config) UnmarshalJSON(bs []byte) error {
	type config struct {
		CREForwarderAddress string        `json:"creForwarderAddress"` // Address of forwarder program
		CREForwarderState   string        `json:"creForwarderState"`   // Address of forwarder program state
		Transmitter         string        `json:"transmitter"`         // Address of forwarder program state
		IsLocal             bool          `json:"isLocal,omitempty"`   // Run against local validator (for local CRE runs only)
		Network             string        `json:"network"`
		ChainID             string        `json:"chainId"`
		DeltaStage          time.Duration `json:"deltaStage"`

		ReadsEnabled                  bool          `json:"readsEnabled"`
		ObservationPollerWorkersCount uint          `json:"observationPollerWorkersCount"`
		ObservationPollPeriod         time.Duration `json:"observationPollPeriod"`
		UnknownRequestsTTL            time.Duration `json:"unknownRequestsTTL"`
	}
	var cfg config

	err := json.Unmarshal(bs, &cfg)
	if err != nil {
		return err
	}

	c.ChainID = cfg.ChainID
	c.IsLocal = cfg.IsLocal
	c.Network = cfg.Network
	c.DeltaStage = cfg.DeltaStage
	c.CREForwarderAddress, err = solana.PublicKeyFromBase58(string(cfg.CREForwarderAddress))
	if err != nil {
		return fmt.Errorf("invalid forwarder address: %w", err)
	}
	c.CREForwarderState, err = solana.PublicKeyFromBase58(string(cfg.CREForwarderState))
	if err != nil {
		return fmt.Errorf("invalid forwarder state address: %w", err)
	}
	c.Transmitter, err = solana.PublicKeyFromBase58(string(cfg.Transmitter))
	if err != nil {
		return fmt.Errorf("invalid transmitter address: %w", err)
	}

	c.ReadsEnabled = cfg.ReadsEnabled
	c.ObservationPollerWorkersCount = cfg.ObservationPollerWorkersCount
	c.ObservationPollPeriod = cfg.ObservationPollPeriod
	c.UnknownRequestsTTL = cfg.UnknownRequestsTTL

	return nil
}
