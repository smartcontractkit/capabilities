package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	relayermock "github.com/smartcontractkit/chainlink-common/pkg/types/core/mocks"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-evm/pkg/testutils"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
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
	cfg := config.Config{ChainID: 1337, Network: "testnet", LogTriggerPollInterval: 60 * time.Second, CREForwarderAddress: common.Bytes2Hex(testutils.NewAddress().Bytes()), ReceiverGasMinimum: 1000}
	cfgJSON, _ := json.Marshal(cfg)

	err := svc.Initialise(context.Background(), string(cfgJSON),
		nil, nil, nil, nil, relayerSet, nil, nil)
	require.NoError(t, err)

	t.Run("happy-path", func(t *testing.T) {
		t.Run("bad-json", func(t *testing.T) {
			svc := &capabilityGRPCService{lggr: lggr}
			err := svc.Initialise(context.Background(), "x", nil, nil, nil, nil, nil, nil, nil)
			assert.ErrorContains(t, err, "failed to parse")
		})
		t.Run("bad-interval", func(t *testing.T) {
			cfgJSON, _ := json.Marshal(config.Config{ChainID: 1, Network: "net", LogTriggerPollInterval: -1})
			svc := &capabilityGRPCService{lggr: lggr}
			err := svc.Initialise(context.Background(), string(cfgJSON), nil, nil, nil, nil, nil, nil, nil)
			assert.ErrorContains(t, err, "LogTriggerPollInterval must be positive, got: -1ns")
		})
		t.Run("relayerSet error", func(t *testing.T) {
			relayerSet := relayermock.NewRelayerSet(t)
			relayerSet.On("Get", mock.Anything, mock.Anything).Return(nil, assert.AnError)

			cfgJSON, _ := json.Marshal(config.Config{ChainID: 1, Network: "net", LogTriggerPollInterval: 60 * time.Second, CREForwarderAddress: common.Bytes2Hex(testutils.NewAddress().Bytes()), ReceiverGasMinimum: 1000})
			svc := &capabilityGRPCService{lggr: lggr}

			err := svc.Initialise(context.Background(), string(cfgJSON),
				nil, nil, nil, nil, relayerSet, nil, nil)
			assert.ErrorIs(t, err, assert.AnError)
		})
	})
	t.Run("Misconfiguration", func(t *testing.T) {
		t.Run("No Keystone forwarder address provided", func(t *testing.T) {
			relayerSet := relayermock.NewRelayerSet(t)
			relayerSet.On("Get", mock.Anything, mock.Anything).Return(nil, assert.AnError)

			cfgJSON, _ := json.Marshal(config.Config{ChainID: 1, Network: "net", ReceiverGasMinimum: 1000})
			svc := &capabilityGRPCService{lggr: lggr}

			err := svc.Initialise(context.Background(), string(cfgJSON),
				nil, nil, nil, nil, relayerSet, nil, nil)
			assert.ErrorIs(t, err, assert.AnError)
		})

		t.Run("ReceiverGasConfig zero", func(t *testing.T) {
			relayerSet := relayermock.NewRelayerSet(t)
			relayerSet.On("Get", mock.Anything, mock.Anything).Return(nil, assert.AnError)

			cfgJSON, _ := json.Marshal(config.Config{ChainID: 1, Network: "net", ReceiverGasMinimum: 1000})
			svc := &capabilityGRPCService{lggr: lggr}

			err := svc.Initialise(context.Background(), string(cfgJSON),
				nil, nil, nil, nil, relayerSet, nil, nil)
			assert.ErrorIs(t, err, assert.AnError)
		})
	})
}
