package metrics

import (
	"context"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

// compositeMetrics implements ConsensusMetrics by wrapping multiple instanced needed to migrate from legacy naming
type compositeMetrics struct {
	metrics []ConsensusMetrics
}

// Ensure compositeMetrics implements ConsensusMetrics
var _ ConsensusMetrics = (*compositeMetrics)(nil)

func (m compositeMetrics) RecordOutcomeChainHeight(ctx context.Context, height *types.ChainHeight) {
	for _, metric := range m.metrics {
		metric.RecordOutcomeChainHeight(ctx, height)
	}
}

func (m compositeMetrics) RecordRoundObservationSize(ctx context.Context, size int) {
	for _, metric := range m.metrics {
		metric.RecordRoundObservationSize(ctx, size)
	}
}

func (m compositeMetrics) RecordRequestObservationSize(ctx context.Context, size int) {
	for _, metric := range m.metrics {
		metric.RecordRequestObservationSize(ctx, size)
	}
}

func (m compositeMetrics) RecordIdenticalResponseCount(ctx context.Context, count int, observationType string) {
	for _, metric := range m.metrics {
		metric.RecordIdenticalResponseCount(ctx, count, observationType)
	}
}

func (m compositeMetrics) RecordQueueSize(ctx context.Context, size int) {
	for _, metric := range m.metrics {
		metric.RecordQueueSize(ctx, size)
	}
}

func (m compositeMetrics) RecordRetryQueueSize(ctx context.Context, size int) {
	for _, metric := range m.metrics {
		metric.RecordRetryQueueSize(ctx, size)
	}
}

func (m compositeMetrics) SetRequestCount(requestCount int) {
	for _, metric := range m.metrics {
		metric.SetRequestCount(requestCount)
	}
}
