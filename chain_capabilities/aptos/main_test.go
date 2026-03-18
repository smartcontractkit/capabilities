package main

import (
	"fmt"
	"testing"

	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"
	"github.com/stretchr/testify/require"
)

func testPeerID(fill byte) p2ptypes.PeerID {
	var id p2ptypes.PeerID
	for i := 0; i < len(id); i++ {
		id[i] = fill
	}
	return id
}

func TestValidateP2PToTransmitterMap_Valid(t *testing.T) {
	p1 := testPeerID(0x01)
	p2 := testPeerID(0x02)

	cfg := map[string]string{
		fmt.Sprintf("%x", p1[:]): "0x1",
		fmt.Sprintf("%x", p2[:]): "0x2",
	}

	require.NoError(t, validateP2PToTransmitterMap([]p2ptypes.PeerID{p1, p2}, cfg))
}

func TestValidateP2PToTransmitterMap_MissingPeerMapping(t *testing.T) {
	p1 := testPeerID(0x01)
	p2 := testPeerID(0x02)

	cfg := map[string]string{
		fmt.Sprintf("%x", p1[:]): "0x1",
	}

	err := validateP2PToTransmitterMap([]p2ptypes.PeerID{p1, p2}, cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "missing mapping for DON member peerID")
}

func TestValidateP2PToTransmitterMap_InvalidAddress(t *testing.T) {
	p1 := testPeerID(0x01)

	cfg := map[string]string{
		fmt.Sprintf("%x", p1[:]): "not-an-address",
	}

	err := validateP2PToTransmitterMap([]p2ptypes.PeerID{p1}, cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid Aptos transmitter")
}

func TestValidateP2PToTransmitterMap_DuplicateTransmitters(t *testing.T) {
	p1 := testPeerID(0x01)
	p2 := testPeerID(0x02)

	cfg := map[string]string{
		fmt.Sprintf("%x", p1[:]): "0x1",
		fmt.Sprintf("%x", p2[:]): "0x1",
	}

	err := validateP2PToTransmitterMap([]p2ptypes.PeerID{p1, p2}, cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "duplicate transmitter address")
}
