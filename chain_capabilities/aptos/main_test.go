package main

import (
	"fmt"
	"testing"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
	chain_selectors "github.com/smartcontractkit/chain-selectors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	commontypes "github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	relayermock "github.com/smartcontractkit/chainlink-common/pkg/types/core/mocks"
	typesmocks "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCapabilityGRPCService_MetadataAndLifecycle(t *testing.T) {
	t.Parallel()

	svc := &capabilityGRPCService{
		lggr:          logger.Test(t),
		chainSelector: 44444,
	}

	require.NoError(t, svc.Start(t.Context()))
	require.NoError(t, svc.Close())
	require.NoError(t, svc.Ready())
	require.Equal(t, uint64(44444), svc.ChainSelector())
	require.Equal(t, "Contains Aptos chain functionalities", svc.Description())
	require.Equal(t, svc.lggr.Name(), svc.Name())
	require.Equal(t, map[string]error{svc.Name(): nil}, svc.HealthReport())

	infos, err := svc.Infos(t.Context())
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
		svc := &capabilityGRPCService{lggr: logger.Test(t)}
		err := svc.setSelector(&config.Config{ChainID: fmt.Sprintf("%d", chainID)})
		require.NoError(t, err)
		require.Equal(t, selector, svc.chainSelector)
	})

	t.Run("invalid chain id", func(t *testing.T) {
		svc := &capabilityGRPCService{lggr: logger.Test(t)}
		err := svc.setSelector(&config.Config{ChainID: "not-a-number"})
		require.ErrorContains(t, err, "failed to parse chainID")
	})

	t.Run("unknown chain id", func(t *testing.T) {
		svc := &capabilityGRPCService{lggr: logger.Test(t)}
		err := svc.setSelector(&config.Config{ChainID: "999999999"})
		require.ErrorContains(t, err, "chain selector not found")
	})
}

func TestCapabilityGRPCService_InitialiseErrors(t *testing.T) {
	t.Parallel()

	var chainID uint64
	for id := range chain_selectors.AptosChainIdToChainSelector() {
		chainID = id
		break
	}
	require.NotZero(t, chainID)

	validConfig := fmt.Sprintf(`{"network":"aptos","chainId":"%d","creForwarderAddress":"0x1","deltaStage":1000000000}`, chainID)
	relayID := commontypes.NewRelayID("aptos", fmt.Sprintf("%d", chainID))

	t.Run("invalid config", func(t *testing.T) {
		svc := &capabilityGRPCService{lggr: logger.Test(t)}
		err := svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{Config: `{`})
		require.ErrorContains(t, err, "failed to unmarshal config")
	})

	t.Run("relayer lookup fails", func(t *testing.T) {
		svc := &capabilityGRPCService{lggr: logger.Test(t)}
		relayerSet := relayermock.NewRelayerSet(t)
		relayerSet.On("Get", mock.Anything, relayID).Return(nil, fmt.Errorf("missing relayer")).Once()

		err := svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
			Config:     validConfig,
			RelayerSet: relayerSet,
		})
		require.ErrorContains(t, err, "failed to fetch relayer")
	})

	t.Run("aptos service fetch fails", func(t *testing.T) {
		svc := &capabilityGRPCService{lggr: logger.Test(t)}
		relayerSet := relayermock.NewRelayerSet(t)
		relayer := relayermock.NewRelayer(t)
		relayerSet.On("Get", mock.Anything, relayID).Return(relayer, nil).Once()
		relayer.On("Aptos").Return(nil, fmt.Errorf("no aptos service")).Once()

		err := svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
			Config:     validConfig,
			RelayerSet: relayerSet,
		})
		require.ErrorContains(t, err, "failed to get aptos service")
	})

	t.Run("chain info fetch fails", func(t *testing.T) {
		svc := &capabilityGRPCService{lggr: logger.Test(t)}
		relayerSet := relayermock.NewRelayerSet(t)
		relayer := relayermock.NewRelayer(t)
		aptosService := typesmocks.NewAptosService(t)
		relayerSet.On("Get", mock.Anything, relayID).Return(relayer, nil).Once()
		relayer.On("Aptos").Return(aptosService, nil).Once()
		relayer.On("GetChainInfo", mock.Anything).Return(commontypes.ChainInfo{}, fmt.Errorf("chain info unavailable")).Once()

		err := svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
			Config:     validConfig,
			RelayerSet: relayerSet,
		})
		require.ErrorContains(t, err, "failed to fetch chain info")
	})
}
