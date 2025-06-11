package main

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/smartcontractkit/chain_capabilities/evm/trigger"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	relayermock "github.com/smartcontractkit/chainlink-common/pkg/types/core/mocks"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
)

func TestCapabilityGRPCService_Initialise(t *testing.T) {
	t.Helper()

	lggr := logger.Test(t)

	evmSvc := evmmock.NewEVMService(t)
	relayer := relayermock.NewRelayer(t)
	relayer.On("EVM").Return(evmSvc, nil)

	relayerSet := relayermock.NewRelayerSet(t)
	relayerSet.On("Get", mock.Anything, mock.Anything).Return(relayer, nil)

	svc := &capabilityGRPCService{lggr: lggr}
	cfg := Config{ChainID: 1337, Network: "testnet", LogTriggerPollInterval: 60 * time.Second}
	cfgJSON, _ := json.Marshal(cfg)

	err := svc.Initialise(context.Background(), string(cfgJSON),
		nil, nil, nil, nil, relayerSet, nil)
	require.NoError(t, err)

	t.Run("happy-path", func(t *testing.T) {
		t.Run("bad-json", func(t *testing.T) {
			svc := &capabilityGRPCService{lggr: lggr}
			err := svc.Initialise(context.Background(), "x", nil, nil, nil, nil, nil, nil)
			assert.ErrorContains(t, err, "failed to parse")
		})
		t.Run("bad-interval", func(t *testing.T) {
			cfgJSON, _ := json.Marshal(Config{ChainID: 1, Network: "net", LogTriggerPollInterval: 1})
			svc := &capabilityGRPCService{lggr: lggr}
			err := svc.Initialise(context.Background(), string(cfgJSON), nil, nil, nil, nil, nil, nil)
			assert.ErrorContains(t, err, "LogTriggerPollInterval must be at least 10s, got: 1ns")
		})
		t.Run("relayerSet error", func(t *testing.T) {
			relayerSet := relayermock.NewRelayerSet(t)
			relayerSet.On("Get", mock.Anything, mock.Anything).Return(nil, assert.AnError)

			cfgJSON, _ := json.Marshal(Config{ChainID: 1, Network: "net", LogTriggerPollInterval: 60 * time.Second})
			svc := &capabilityGRPCService{lggr: lggr}

			err := svc.Initialise(context.Background(), string(cfgJSON),
				nil, nil, nil, nil, relayerSet, nil)
			assert.ErrorIs(t, err, assert.AnError)
		})

		t.Run("close", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			evmSvc.On("LatestAndFinalizedHead", mock.Anything).Return(evmtypes.Head{Number: big.NewInt(30)}, evmtypes.Head{Number: big.NewInt(25)}, nil)
			evmSvc.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
			evmSvc.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()

			lggr := logger.Nop() //making sure it doesn't panic when logging in the thread
			svc := &capabilityGRPCService{lggr: lggr}
			cfg := Config{ChainID: 1337, Network: "testnet", LogTriggerPollInterval: 60 * time.Second}
			cfgJSON, _ := json.Marshal(cfg)
			err := svc.Initialise(context.Background(), string(cfgJSON), nil, nil, nil, nil, relayerSet, nil)
			require.NoError(t, err)

			store := trigger.NewLogTriggerStore()
			svc.triggerService = trigger.NewLogTriggerService(evmSvc, store, lggr, 60*time.Second)

			triggerID := "triggerID"
			_, exists := store.Read(triggerID)
			assert.False(t, exists, "Trigger should not exist before registration")

			_, err = svc.RegisterLogTrigger(ctx, triggerID, capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{
				Addresses: [][]byte{{0xde}},
				EventSigs: [][]byte{{0xdd}},
			})
			require.NoError(t, err)

			_, exists = store.Read(triggerID)
			assert.True(t, exists, "Trigger should exist after registration")

			err = svc.Close()
			assert.NoError(t, err)
			cancel()

			_, exists = store.Read(triggerID)
			assert.False(t, exists, "Trigger should not exist after close")
		})
	})
}
