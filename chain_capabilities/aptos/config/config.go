package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
)

type Config struct {
	CREForwarderAddress           [32]byte          // 32-byte Aptos account address of forwarder module
	IsLocal                       bool              `json:"isLocal"`
	DeltaStage                    time.Duration     // DeltaStage for staggered transmission scheduling
	ObservationPollerWorkersCount uint              `json:"observationPollerWorkersCount"`
	ObservationPollPeriod         time.Duration     `json:"observationPollPeriod"`
	ChainHeightPollPeriod         time.Duration     `json:"chainHeightPollPeriod"`
	UnknownRequestsTTL            time.Duration     `json:"unknownRequestsTTL"`
	Network                       string            `json:"network"`
	ChainID                       string            `json:"chainId"`
	P2PToTransmitterMap           map[string]string // peerID-hex → Aptos transmitter address, populated from specConfig
}

func (c *Config) UnmarshalJSON(bs []byte) error {
	type config struct {
		CREForwarderAddress           string            `json:"creForwarderAddress"` // hex-encoded address (with or without 0x prefix)
		IsLocal                       any               `json:"isLocal"`
		DeltaStage                    time.Duration     `json:"deltaStage"`
		ObservationPollerWorkersCount uint              `json:"observationPollerWorkersCount"`
		ObservationPollPeriod         time.Duration     `json:"observationPollPeriod"`
		ChainHeightPollPeriod         time.Duration     `json:"chainHeightPollPeriod"`
		UnknownRequestsTTL            time.Duration     `json:"unknownRequestsTTL"`
		Network                       string            `json:"network"`
		ChainID                       string            `json:"chainId"`
		P2PToTransmitterMap           map[string]string `json:"p2pToTransmitterMap,omitempty"`
	}
	var cfg config

	if err := json.Unmarshal(bs, &cfg); err != nil {
		return err
	}

	c.ChainID = cfg.ChainID
	switch v := cfg.IsLocal.(type) {
	case nil:
		c.IsLocal = false
	case bool:
		c.IsLocal = v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("invalid isLocal value %q: %w", v, err)
		}
		c.IsLocal = parsed
	default:
		return fmt.Errorf("invalid isLocal type %T", v)
	}
	c.DeltaStage = cfg.DeltaStage
	c.ObservationPollerWorkersCount = cfg.ObservationPollerWorkersCount
	c.ObservationPollPeriod = cfg.ObservationPollPeriod
	c.ChainHeightPollPeriod = cfg.ChainHeightPollPeriod
	c.UnknownRequestsTTL = cfg.UnknownRequestsTTL
	c.Network = cfg.Network
	c.P2PToTransmitterMap = cfg.P2PToTransmitterMap

	addr, err := aptos_sdk.ConvertToAddress(cfg.CREForwarderAddress)
	if err != nil {
		return fmt.Errorf("invalid forwarder address %q: %w", cfg.CREForwarderAddress, err)
	}
	c.CREForwarderAddress = [32]byte(*addr)

	return nil
}
