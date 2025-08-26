package actions

import (
	"testing"

	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions/mocks"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/test"
)

type EvmWithMocks struct {
	EVM
	EvmService       *evmmock.EVMService
	ConsensusHandler *mocks.ConsensusHandler
}

func InitMocks(t *testing.T) *EvmWithMocks {
	t.Helper()
	evmSvc := evmmock.NewEVMService(t)
	consensusHandler := mocks.NewConsensusHandler(t)
	evm, err := NewEVM(config.Config{}, evmSvc, commonlogger.Test(t), test.NopBeholderProcessor{}, &monitoring.MessageBuilder{}, consensusHandler)
	require.NoError(t, err)
	return &EvmWithMocks{
		EVM:              evm,
		EvmService:       evmSvc,
		ConsensusHandler: consensusHandler,
	}
}
