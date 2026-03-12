package actions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/test"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions/mocks"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
)

type EvmWithMocks struct {
	*EVM
	EvmService       *evmmock.EVMService
	ConsensusHandler *mocks.ConsensusHandler
}

func InitMocks(t *testing.T) *EvmWithMocks {
	t.Helper()
	evmSvc := evmmock.NewEVMService(t)
	consensusHandler := mocks.NewConsensusHandler(t)
	lggr := logger.Test(t)
	randomEVMAddress := "0xFc5df03D4E91bae4c118B7dda995476f332C9d8C"
	evm, err := NewEVM(config.Config{CREForwarderAddress: randomEVMAddress}, evmSvc, lggr, test.NopBeholderProcessor{}, monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""), consensusHandler, 1, limits.Factory{Logger: lggr}, ts.TransmissionScheduler{})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, evm.Close())
	})
	return &EvmWithMocks{
		EVM:              evm,
		EvmService:       evmSvc,
		ConsensusHandler: consensusHandler,
	}
}
