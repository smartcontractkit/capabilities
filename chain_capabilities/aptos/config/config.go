package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	aptossdk "github.com/aptos-labs/aptos-go-sdk"
)

type Config struct {
	CREForwarderAddress aptossdk.AccountAddress // Aptos account address of forwarder module
	Network             string                  `json:"network"`
	ChainID             string                  `json:"chainId"`
	IsLocal             bool                    `json:"isLocal,omitempty"` // Run against local node (for local CRE runs only)
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

	addr, err := parseAddress(cfg.CREForwarderAddress)
	if err != nil {
		return fmt.Errorf("invalid forwarder address: %w", err)
	}
	c.CREForwarderAddress = addr

	return c.Validate()
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.Network) == "" {
		return fmt.Errorf("network is required")
	}
	if strings.TrimSpace(c.ChainID) == "" {
		return fmt.Errorf("chainId is required")
	}
	if !c.IsLocal {
		if _, err := strconv.ParseUint(c.ChainID, 10, 64); err != nil {
			return fmt.Errorf("chainId must be an unsigned integer for Aptos: %w", err)
		}
	}
	return nil
}

func parseAddress(s string) (aptossdk.AccountAddress, error) {
	if strings.TrimSpace(s) == "" {
		return aptossdk.AccountAddress{}, fmt.Errorf("address is required")
	}
	var addr aptossdk.AccountAddress
	if err := addr.ParseStringRelaxed(s); err != nil {
		return aptossdk.AccountAddress{}, fmt.Errorf("failed to parse Aptos address: %w", err)
	}
	return addr, nil
}
