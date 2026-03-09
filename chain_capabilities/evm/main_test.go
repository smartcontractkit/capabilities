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
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	relayermock "github.com/smartcontractkit/chainlink-common/pkg/types/core/mocks"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-evm/pkg/testutils"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
)

func TestCapabilityGRPCService_Initialise(t *testing.T) {
	t.Parallel()
	t.Run("happy-path", func(t *testing.T) {
		evmSvc := evmmock.NewEVMService(t)
		evmSvc.On("GetFiltersNames", mock.Anything).Maybe().Return([]string{}, nil)
		relayer := relayermock.NewRelayer(t)
		relayer.On("EVM").Return(evmSvc, nil)
		relayer.On("GetChainInfo", mock.Anything).Return(types.ChainInfo{}, nil)

		relayerSet := relayermock.NewRelayerSet(t)
		relayerSet.On("Get", mock.Anything, mock.Anything).Return(relayer, nil)
		svc := &capabilityGRPCService{lggr: logger.Test(t)}
		cfg := config.Config{ChainID: 1337, Network: "testnet", LogTriggerPollInterval: 60 * time.Second, CREForwarderAddress: testutils.NewAddress().String(), ReceiverGasMinimum: 1000}
		cfgJSON, _ := json.Marshal(cfg)

		err := svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{OracleFactory: nullOracleFactory{}, RelayerSet: relayerSet, Config: string(cfgJSON)})
		require.NoError(t, err)
		require.NoError(t, svc.Close())
	})
	t.Run("happy-path-with-triggers-params", func(t *testing.T) {
		evmSvc := evmmock.NewEVMService(t)
		evmSvc.On("GetFiltersNames", mock.Anything).Maybe().Return([]string{}, nil)
		relayer := relayermock.NewRelayer(t)
		relayer.On("EVM").Return(evmSvc, nil)
		relayer.On("GetChainInfo", mock.Anything).Return(types.ChainInfo{}, nil)

		relayerSet := relayermock.NewRelayerSet(t)
		relayerSet.On("Get", mock.Anything, mock.Anything).Return(relayer, nil)
		svc := &capabilityGRPCService{lggr: logger.Test(t)}
		cfg := config.Config{ChainID: 1337, Network: "testnet", LogTriggerPollInterval: 60 * time.Second, LogTriggerSendChannelBufferSize: 100, LogTriggerLimitQueryLogSize: 10, CREForwarderAddress: common.Bytes2Hex(testutils.NewAddress().Bytes()), ReceiverGasMinimum: 1000}
		cfgJSON, _ := json.Marshal(cfg)

		err := svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{OracleFactory: nullOracleFactory{}, RelayerSet: relayerSet, Config: string(cfgJSON)})
		require.NoError(t, err)
		require.NoError(t, svc.Close())
	})
	t.Run("bad-json", func(t *testing.T) {
		svc := &capabilityGRPCService{lggr: logger.Test(t)}
		err := svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{OracleFactory: nullOracleFactory{}, Config: "x"})

		assert.ErrorContains(t, err, "failed to parse")
	})
	t.Run("bad-trigger-params", func(t *testing.T) {
		evmSvc := evmmock.NewEVMService(t)
		evmSvc.On("GetFiltersNames", mock.Anything).Maybe().Return([]string{}, nil)
		relayer := relayermock.NewRelayer(t)
		relayer.On("EVM").Return(evmSvc, nil)
		relayer.On("GetChainInfo", mock.Anything).Return(types.ChainInfo{}, nil)

		relayerSet := relayermock.NewRelayerSet(t)
		relayerSet.On("Get", mock.Anything, mock.Anything).Return(relayer, nil)
		svc := &capabilityGRPCService{lggr: logger.Test(t)}

		cfg := config.Config{ChainID: 1337, Network: "testnet", LogTriggerPollInterval: -1, CREForwarderAddress: common.Bytes2Hex(testutils.NewAddress().Bytes()), ReceiverGasMinimum: 1000}
		cfgJSON, _ := json.Marshal(cfg)
		err := svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{OracleFactory: nullOracleFactory{}, RelayerSet: relayerSet, Config: string(cfgJSON)})

		assert.ErrorContains(t, err, "failed to unmarshal config: logTriggerPollInterval must be positive, got: -1ns")

		cfg = config.Config{ChainID: 1337, Network: "testnet", LogTriggerPollInterval: 60 * time.Second, CREForwarderAddress: common.Bytes2Hex(testutils.NewAddress().Bytes()), ReceiverGasMinimum: 1000, LogTriggerLimitQueryLogSize: uint64(1001)}
		cfgJSON, _ = json.Marshal(cfg)
		err = svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{OracleFactory: nullOracleFactory{}, RelayerSet: relayerSet, Config: string(cfgJSON)})
		assert.ErrorContains(t, err, "error when creating trigger: logTriggerLimitQueryLogSize (1001) must be less than logTriggerSendChannelBufferSize (1000)")

		cfg = config.Config{ChainID: 1337, Network: "testnet", LogTriggerPollInterval: 60 * time.Second, CREForwarderAddress: common.Bytes2Hex(testutils.NewAddress().Bytes()), ReceiverGasMinimum: 1000,
			LogTriggerSendChannelBufferSize: 5, LogTriggerLimitQueryLogSize: uint64(10)}
		cfgJSON, _ = json.Marshal(cfg)
		err = svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{OracleFactory: nullOracleFactory{}, RelayerSet: relayerSet, Config: string(cfgJSON)})
		assert.ErrorContains(t, err, "error when creating trigger: logTriggerLimitQueryLogSize (10) must be less than logTriggerSendChannelBufferSize (5)")
	})
	t.Run("relayerSet error", func(t *testing.T) {
		relayerSet := relayermock.NewRelayerSet(t)
		relayerSet.On("Get", mock.Anything, mock.Anything).Return(nil, assert.AnError)

		cfgJSON, _ := json.Marshal(config.Config{ChainID: 1, Network: "net", LogTriggerPollInterval: 60 * time.Second, CREForwarderAddress: testutils.NewAddress().String(), ReceiverGasMinimum: 1000})
		svc := &capabilityGRPCService{lggr: logger.Test(t)}

		err := svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{RelayerSet: relayerSet, Config: string(cfgJSON)})

		assert.ErrorIs(t, err, assert.AnError)
	})
	t.Run("Misconfiguration", func(t *testing.T) {
		t.Run("No Keystone forwarder address provided", func(t *testing.T) {
			cfgJSON, _ := json.Marshal(config.Config{ChainID: 1, Network: "net", ReceiverGasMinimum: 1000})
			svc := &capabilityGRPCService{lggr: logger.Test(t)}

			err := svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{Config: string(cfgJSON)})

			assert.ErrorContains(t, err, "invalid cre forward address, it does not have 20 characters")
		})

		t.Run("ReceiverGasConfig zero", func(t *testing.T) {
			cfgJSON, _ := json.Marshal(config.Config{ChainID: 1, Network: "net", ReceiverGasMinimum: 0, CREForwarderAddress: testutils.NewAddress().String()})
			svc := &capabilityGRPCService{lggr: logger.Test(t)}

			err := svc.Initialise(t.Context(), core.StandardCapabilitiesDependencies{Config: string(cfgJSON)})
			assert.ErrorContains(t, err, "invalid ReceiverGasMinimum value. It must be greater than 0. Provided ReceiverGasMinimum 0")
		})
	})
}

type nullOracleFactory struct{}

func (nullOracleFactory) NewOracle(ctx context.Context, args core.OracleArgs) (core.Oracle, error) {
	return nullOracle{}, nil
}

type nullOracle struct{}

func (nullOracle) Start(ctx context.Context) error {
	return nil
}

func (nullOracle) Close(ctx context.Context) error {
	return nil
}
