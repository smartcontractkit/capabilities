package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
			cfgJSON, _ := json.Marshal(Config{ChainID: 1, Network: "net", LogTriggerPollInterval: -1})
			svc := &capabilityGRPCService{lggr: lggr}
			err := svc.Initialise(context.Background(), string(cfgJSON), nil, nil, nil, nil, nil, nil)
			assert.ErrorContains(t, err, "LogTriggerPollInterval must be positive, got: -1ns")
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
	})
}
