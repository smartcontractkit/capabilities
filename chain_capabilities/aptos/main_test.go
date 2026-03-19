package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	chain_selectors "github.com/smartcontractkit/chain-selectors"
	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
)

type testCapabilitiesRegistry struct {
	core.UnimplementedCapabilitiesRegistry
	cfg commoncap.CapabilityConfiguration
	err error
}

func (t testCapabilitiesRegistry) ConfigForCapability(context.Context, string, uint32) (commoncap.CapabilityConfiguration, error) {
	return t.cfg, t.err
}

func testPeerID(fill byte) p2ptypes.PeerID {
	var id p2ptypes.PeerID
	for i := 0; i < len(id); i++ {
		id[i] = fill
	}
	return id
}

func TestCapabilityGRPCService_MetadataAndLifecycle(t *testing.T) {
	t.Parallel()

	c := &capabilityGRPCService{
		lggr:          logger.Test(t),
		chainSelector: 44444,
	}

	require.NoError(t, c.Start(t.Context()))
	require.NoError(t, c.Close())
	require.NoError(t, c.Ready())
	require.Equal(t, "Contains Aptos chain functionalities", c.Description())
	require.Equal(t, c.lggr.Name(), c.Name())
	require.Equal(t, map[string]error{c.Name(): nil}, c.HealthReport())

	infos, err := c.Infos(t.Context())
	require.NoError(t, err)
	require.Len(t, infos, 1)
	require.Equal(t, capabilityID(44444), infos[0].ID)
}

func TestCapabilityGRPCService_SetSelector(t *testing.T) {
	t.Parallel()

	var (
		chainID  uint64
		selector uint64
	)
	for id, sel := range chain_selectors.AptosChainIdToChainSelector() {
		chainID = id
		selector = sel
		break
	}
	require.NotZero(t, chainID)
	require.NotZero(t, selector)

	t.Run("valid", func(t *testing.T) {
		c := &capabilityGRPCService{lggr: logger.Test(t)}
		err := c.setSelector(&config.Config{ChainID: fmt.Sprintf("%d", chainID)})
		require.NoError(t, err)
		require.Equal(t, selector, c.chainSelector)
	})

	t.Run("invalid chain id", func(t *testing.T) {
		c := &capabilityGRPCService{lggr: logger.Test(t)}
		err := c.setSelector(&config.Config{ChainID: "not-a-number"})
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to parse chainID")
	})

	t.Run("unknown chain id", func(t *testing.T) {
		c := &capabilityGRPCService{lggr: logger.Test(t)}
		err := c.setSelector(&config.Config{ChainID: "999999999"})
		require.Error(t, err)
		require.ErrorContains(t, err, "chain selector not found")
	})
}

func TestCapabilityGRPCService_UnmarshalConfig(t *testing.T) {
	t.Parallel()

	c := &capabilityGRPCService{lggr: logger.Test(t)}

	cfg, err := c.unmarshalConfig(`{"network":"aptos","chainId":"1","creForwarderAddress":"0x1","deltaStage":1000000000}`)
	require.NoError(t, err)
	require.Equal(t, "aptos", cfg.Network)
	require.Equal(t, "1", cfg.ChainID)
	require.Equal(t, time.Second, cfg.DeltaStage)

	_, err = c.unmarshalConfig(`{"network":"aptos","chainId":"1","creForwarderAddress":"zz"}`)
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid forwarder address")

	_, err = c.unmarshalConfig(`{`)
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to parse Aptos capability config")
}

func TestCapabilityGRPCService_FetchP2PConfig(t *testing.T) {
	t.Parallel()

	newService := func(t *testing.T) *capabilityGRPCService {
		t.Helper()
		return &capabilityGRPCService{
			lggr: logger.Test(t),
			capability: capability{
				id: "aptos:ChainSelector:1@1.0.0",
			},
			CapabilityInfo: commoncap.CapabilityInfo{
				DON: &commoncap.DON{ID: 7},
			},
		}
	}

	t.Run("success", func(t *testing.T) {
		spec, err := values.NewMap(map[string]any{
			"p2pToTransmitterMap": map[string]any{
				"peer-a": "0x1",
				"peer-b": "0x2",
			},
		})
		require.NoError(t, err)

		got, err := newService(t).fetchP2PConfig(t.Context(), testCapabilitiesRegistry{
			cfg: commoncap.CapabilityConfiguration{SpecConfig: spec},
		})
		require.NoError(t, err)
		require.Equal(t, map[string]string{"peer-a": "0x1", "peer-b": "0x2"}, got)
	})

	t.Run("nil spec config", func(t *testing.T) {
		_, err := newService(t).fetchP2PConfig(t.Context(), testCapabilitiesRegistry{})
		require.Error(t, err)
		require.ErrorContains(t, err, "SpecConfig is nil")
	})

	t.Run("missing p2p map", func(t *testing.T) {
		spec, err := values.NewMap(map[string]any{"other": "value"})
		require.NoError(t, err)

		_, err = newService(t).fetchP2PConfig(t.Context(), testCapabilitiesRegistry{
			cfg: commoncap.CapabilityConfiguration{SpecConfig: spec},
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "p2pToTransmitterMap not found")
	})

	t.Run("non string value", func(t *testing.T) {
		spec, err := values.NewMap(map[string]any{
			"p2pToTransmitterMap": map[string]any{
				"peer-a": 123,
			},
		})
		require.NoError(t, err)

		_, err = newService(t).fetchP2PConfig(t.Context(), testCapabilitiesRegistry{
			cfg: commoncap.CapabilityConfiguration{SpecConfig: spec},
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "expected string")
	})
}

func TestValidateP2PToTransmitterMap_Valid(t *testing.T) {
	t.Parallel()

	p1 := testPeerID(0x01)
	p2 := testPeerID(0x02)

	cfg := map[string]string{
		fmt.Sprintf("%x", p1[:]): "0x1",
		fmt.Sprintf("%x", p2[:]): "0x2",
	}

	require.NoError(t, validateP2PToTransmitterMap([]p2ptypes.PeerID{p1, p2}, cfg))
}

func TestValidateP2PToTransmitterMap_MissingPeerMapping(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	p1 := testPeerID(0x01)

	cfg := map[string]string{
		fmt.Sprintf("%x", p1[:]): "not-an-address",
	}

	err := validateP2PToTransmitterMap([]p2ptypes.PeerID{p1}, cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid Aptos transmitter")
}

func TestValidateP2PToTransmitterMap_DuplicateTransmitters(t *testing.T) {
	t.Parallel()

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
