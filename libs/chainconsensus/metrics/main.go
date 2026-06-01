package metrics

import (
	"context"
	"fmt"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/types"
)

type ConsensusMetrics interface {
	// metrics for consensus' reporting_plugin
	RecordOutcomeChainHeight(ctx context.Context, height *ctypes.ChainHeight)
	RecordRoundObservationSize(ctx context.Context, size int)
	RecordRequestObservationSize(ctx context.Context, size int)
	RecordIdenticalResponseCount(ctx context.Context, count int, observationType string)

	// metrics for consensus' poller
	RecordQueueSize(ctx context.Context, size int)
	RecordRetryQueueSize(ctx context.Context, size int)

	// metrics for consensus' handler
	SetRequestCount(requestCount int)
}

// prefixes holds the metric name prefixes for legacy and new naming conventions
// "evm_" is prefix is deprecated and will be removed, once we release to all envs and migrate chainlink-observability
var prefixes = []string{"evm_", "chain_"}

func MetricViews() []sdkmetric.View {
	type instrumentView struct {
		name       string
		boundaries []float64
	}
	instruments := []instrumentView{
		{
			name:       "capability_consensus_round_observation_size",
			boundaries: []float64{0, 10, 100, 1000, 10000, 100000, 1000000},
		},
		{
			name:       "capability_consensus_request_observation_size",
			boundaries: []float64{0, 10, 100, 1000, 10000, 100000, 1000000},
		},
		{
			name:       "capability_consensus_identical_response_count",
			boundaries: []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		},
	}
	var views []sdkmetric.View
	for _, prefix := range prefixes {
		for _, inst := range instruments {
			views = append(views, sdkmetric.NewView(
				sdkmetric.Instrument{Name: prefix + inst.name},
				sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
					Boundaries: inst.boundaries,
				}},
			))
		}
	}
	return views
}

func NewConsensusMetrics(chainInfo types.ChainInfo) (ConsensusMetrics, error) {
	var metrics []ConsensusMetrics
	for _, prefix := range prefixes {
		m, err := newConsensusMetrics(chainInfo, prefix)
		if err != nil {
			return nil, fmt.Errorf("error constructing consensus metrics for prefix %s: %w", prefix, err)
		}
		metrics = append(metrics, m)
	}

	return &compositeMetrics{metrics: metrics}, nil
}
