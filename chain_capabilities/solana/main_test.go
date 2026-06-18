package main

import (
	"testing"

	chain_selectors "github.com/smartcontractkit/chain-selectors"

	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/config"
)

func TestLocalChainInfo(t *testing.T) {
	t.Parallel()

	selector := chain_selectors.TEST_22222222222222222222222222222222222222222222.Selector
	info := localChainInfo(&config.Config{
		ChainID: "local-genesis-hash",
		Network: "devnet",
	}, selector)

	if info.FamilyName != "solana" {
		t.Fatalf("FamilyName = %q, want solana", info.FamilyName)
	}
	if info.ChainID != "local-genesis-hash" {
		t.Fatalf("ChainID = %q, want local-genesis-hash", info.ChainID)
	}
	if info.NetworkName != "devnet" {
		t.Fatalf("NetworkName = %q, want devnet", info.NetworkName)
	}
	if info.NetworkNameFull != "devnet-local" {
		t.Fatalf("NetworkNameFull = %q, want devnet-local", info.NetworkNameFull)
	}
}

func TestLocalChainInfoDefaults(t *testing.T) {
	t.Parallel()

	selector := uint64(12345)
	info := localChainInfo(&config.Config{}, selector)

	if info.ChainID != "12345" {
		t.Fatalf("ChainID = %q, want 12345", info.ChainID)
	}
	if info.NetworkName != "local" {
		t.Fatalf("NetworkName = %q, want local", info.NetworkName)
	}
	if info.NetworkNameFull != "local-local" {
		t.Fatalf("NetworkNameFull = %q, want local-local", info.NetworkNameFull)
	}
}
