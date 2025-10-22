package metrics_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/metrics"
	ctypes "github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
)

func TestEvmConsensusMetrics_AllMetrics(t *testing.T) {
	m, err := metrics.NewEvmConsensusMetrics("fake-chain-id")
	assert.NoError(t, err)
	ctx := t.Context()

	m.RecordOutcomeChainHeight(ctx, nil) // nil scenario
	m.RecordOutcomeChainHeight(ctx, &ctypes.ChainHeight{Safe: 1, Latest: 2, Finalized: 3})

	m.RecordRoundObservationSize(ctx, 42)

	m.RecordRequestObservationSize(ctx, 84)

	m.RecordQueueSize(ctx, 7)

	m.RecordRetryQueueSize(ctx, 13)

	m.SetRequestCount(99)
}
