package config

import (
	"encoding/json"
	"fmt"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
)

type Config struct {
	CREForwarderAddress  [32]byte                       // 32-byte Aptos account address of forwarder module
	TransmissionSchedule transmission_schedule.Schedule `json:"transmissionSchedule"`
	DeltaStage           time.Duration                  // DeltaStage for staggered transmission scheduling
	Network              string                         `json:"network"`
	ChainID              string                         `json:"chainId"`
	P2PToTransmitterMap  map[string]string              // peerID-hex -> Aptos transmitter address, populated from specConfig
}

func (c *Config) UnmarshalJSON(bs []byte) error {
	type config struct {
		CREForwarderAddress  string            `json:"creForwarderAddress"` // hex-encoded address (with or without 0x prefix)
		TransmissionSchedule string            `json:"transmissionSchedule"`
		DeltaStage           time.Duration     `json:"deltaStage"`
		Network              string            `json:"network"`
		ChainID              string            `json:"chainId"`
		P2PToTransmitterMap  map[string]string `json:"p2pToTransmitterMap,omitempty"`
	}
	var cfg config

	if err := json.Unmarshal(bs, &cfg); err != nil {
		return err
	}

	schedule, err := transmission_schedule.ParseSchedule(cfg.TransmissionSchedule)
	if err != nil {
		return fmt.Errorf("invalid transmissionSchedule %q: %w", cfg.TransmissionSchedule, err)
	}

	c.ChainID = cfg.ChainID
	c.TransmissionSchedule = schedule
	c.DeltaStage = cfg.DeltaStage
	c.Network = cfg.Network
	c.P2PToTransmitterMap = cfg.P2PToTransmitterMap

	addr, err := aptos_sdk.ConvertToAddress(cfg.CREForwarderAddress)
	if err != nil {
		return fmt.Errorf("invalid forwarder address %q: %w", cfg.CREForwarderAddress, err)
	}
	c.CREForwarderAddress = [32]byte(*addr)

	return nil
}
