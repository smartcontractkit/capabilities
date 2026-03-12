package config

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type Config struct {
	CREForwarderAddress  [32]byte          // 32-byte Aptos account address of forwarder module
	DeltaStage           time.Duration     // DeltaStage for staggered transmission scheduling
	Network              string            `json:"network"`
	ChainID              string            `json:"chainId"`
	P2PToTransmitterMap  map[string]string // peerID-hex → Aptos transmitter address, populated from specConfig
}

func (c *Config) UnmarshalJSON(bs []byte) error {
	type config struct {
		CREForwarderAddress string            `json:"creForwarderAddress"` // hex-encoded 32-byte address
		DeltaStage          time.Duration     `json:"deltaStage"`
		Network             string            `json:"network"`
		ChainID             string            `json:"chainId"`
		P2PToTransmitterMap map[string]string `json:"p2pToTransmitterMap,omitempty"`
	}
	var cfg config

	if err := json.Unmarshal(bs, &cfg); err != nil {
		return err
	}

	c.ChainID = cfg.ChainID
	c.DeltaStage = cfg.DeltaStage
	c.Network = cfg.Network
	c.P2PToTransmitterMap = cfg.P2PToTransmitterMap

	addr, err := parseHexAddress(cfg.CREForwarderAddress)
	if err != nil {
		return fmt.Errorf("invalid forwarder address: %w", err)
	}
	c.CREForwarderAddress = addr

	return nil
}

func parseHexAddress(s string) ([32]byte, error) {
	// Strip optional 0x prefix
	if len(s) >= 2 && s[:2] == "0x" {
		s = s[2:]
	}

	// Pad left with zeros if needed (Aptos addresses can be short)
	for len(s) < 64 {
		s = "0" + s
	}

	b, err := hex.DecodeString(s)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to decode hex address: %w", err)
	}

	if len(b) != 32 {
		return [32]byte{}, fmt.Errorf("expected 32 bytes, got %d", len(b))
	}

	var addr [32]byte
	copy(addr[:], b)
	return addr, nil
}
