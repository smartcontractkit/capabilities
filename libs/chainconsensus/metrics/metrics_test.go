package metrics_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	commontTypes "github.com/smartcontractkit/chainlink-common/pkg/types"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

func TestEvmConsensusMetrics_AllMetrics(t *testing.T) {
	m, err := metrics.NewConsensusMetrics(commontTypes.ChainInfo{ChainID: "fake-chain-id"})
	assert.NoError(t, err)
	ctx := t.Context()

	m.RecordOutcomeChainHeight(ctx, nil) // nil scenario
	m.RecordOutcomeChainHeight(ctx, &ctypes.ChainHeight{Safe: 1, Latest: 2, Finalized: 3})

	m.RecordRoundObservationSize(ctx, 42)

	m.RecordRequestObservationSize(ctx, 84)

	m.RecordIdenticalResponseCount(ctx, 5, "EVENTUALLY_CONSISTENT")

	m.RecordQueueSize(ctx, 7)

	m.RecordRetryQueueSize(ctx, 13)

	m.SetRequestCount(99)
}
