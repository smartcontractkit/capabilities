package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	chain_selectors "github.com/smartcontractkit/chain-selectors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	commontypes "github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	relayermock "github.com/smartcontractkit/chainlink-common/pkg/types/core/mocks"
	typesmocks "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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

func TestCapabilityGRPCService_UnmarshalConfig_DefaultsConsensusSettings(t *testing.T) {
	t.Parallel()

	var chainID uint64
	for id := range chain_selectors.AptosChainIdToChainSelector() {
		chainID = id
		break
	}
	require.NotZero(t, chainID)

	raw, err := json.Marshal(map[string]any{
		"network":             "aptos",
		"chainId":             fmt.Sprintf("%d", chainID),
		"creForwarderAddress": "0x1",
	})
	require.NoError(t, err)

	svc := &capabilityGRPCService{lggr: logger.Test(t)}
	cfg, err := svc.unmarshalConfig(string(raw))
	require.NoError(t, err)
	require.EqualValues(t, defaultObservationWorkers, cfg.ObservationPollerWorkersCount)
	require.Equal(t, defaultObservationPollPeriod, cfg.ObservationPollPeriod)
	require.Equal(t, defaultChainHeightPollPeriod, cfg.ChainHeightPollPeriod)
	require.Equal(t, defaultUnknownRequestsTTL, cfg.UnknownRequestsTTL)
	require.Equal(t, time.Duration(0), cfg.DeltaStage)
}
