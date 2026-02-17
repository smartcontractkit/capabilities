package config

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type Config struct {
	CREForwarderAddress [32]byte // 32-byte Aptos account address of forwarder module
	Network             string   `json:"network"`
	ChainID             string   `json:"chainId"`
	IsLocal             bool     `json:"isLocal,omitempty"` // Run against local node (for local CRE runs only)
}

func (c *Config) UnmarshalJSON(bs []byte) error {
	type config struct {
		CREForwarderAddress string `json:"creForwarderAddress"` // hex-encoded 32-byte address
		Network             string `json:"network"`
		ChainID             string `json:"chainId"`
		IsLocal             bool   `json:"isLocal,omitempty"`
	}
	var cfg config

	if err := json.Unmarshal(bs, &cfg); err != nil {
		return err
	}

	c.ChainID = cfg.ChainID
	c.IsLocal = cfg.IsLocal
	c.Network = cfg.Network

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
