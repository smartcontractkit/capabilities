package actions

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions/mocks"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/test"
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
	evm, err := NewEVM(config.Config{CREForwarderAddress: randomEVMAddress}, evmSvc, lggr, test.NopBeholderProcessor{}, &monitoring.MessageBuilder{}, consensusHandler, 1, limits.Factory{Logger: lggr})
	require.NoError(t, err)
	return &EvmWithMocks{
		EVM:              evm,
		EvmService:       evmSvc,
		ConsensusHandler: consensusHandler,
	}
}
