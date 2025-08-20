package trigger

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const (
	AttrNodeAddress = "node_address"
)

// Metrics contains metrics for HTTP triggers
type Metrics struct {
	registerCount             metric.Int64Counter
	deregisterCount           metric.Int64Counter
	registerFailureCount      metric.Int64Counter
	deregisterFailureCount    metric.Int64Counter
	requestCacheCleanUpCount  metric.Int64Counter
	requestCount              metric.Int64Counter
	gatewayGlobalThrottled    metric.Int64Counter
	gatewayNodeThrottled      metric.Int64Counter
	requestSuccessCount       metric.Int64Counter
	gatewaySendError          metric.Int64Counter
	broadcastMetadataCount    metric.Int64Counter
	broadcastMetadataFailures metric.Int64Counter
	broadcastMetadataLatency  metric.Int64Histogram
	pullMetadataCount         metric.Int64Counter
	pullMetadataFailures      metric.Int64Counter
	pullMetadataLatency       metric.Int64Histogram
	requestLatency            metric.Int64Histogram

	once sync.Once
	err  error
}

// NewMetrics creates a new instance of Metrics
func NewMetrics() *Metrics {
	m := &Metrics{}
	m.init()
	return m
}

func (m *Metrics) init() {
	meter := beholder.GetMeter()

	m.registerCount, m.err = meter.Int64Counter(
		"http_trigger_register_count",
		metric.WithDescription("Number of HTTP trigger registrations"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger register count metric: %w", m.err)
		return
	}

	m.deregisterCount, m.err = meter.Int64Counter(
		"http_trigger_deregister_count",
		metric.WithDescription("Number of HTTP trigger deregistrations"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger deregister count metric: %w", m.err)
		return
	}

	m.registerFailureCount, m.err = meter.Int64Counter(
		"http_trigger_register_failure_count",
		metric.WithDescription("Number of HTTP trigger registration failures"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger register failure count metric: %w", m.err)
		return
	}

	m.deregisterFailureCount, m.err = meter.Int64Counter(
		"http_trigger_deregister_failure_count",
		metric.WithDescription("Number of HTTP trigger deregistration failures"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger deregister failure count metric: %w", m.err)
		return
	}

	m.requestCacheCleanUpCount, m.err = meter.Int64Counter(
		"http_trigger_request_cache_cleanup_count",
		metric.WithDescription("Number of expired entries cleaned up from HTTP trigger request cache"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger request cache cleanup count metric: %w", m.err)
		return
	}

	m.requestCount, m.err = meter.Int64Counter(
		"http_trigger_capability_request_count",
		metric.WithDescription("Number of HTTP trigger requests processed"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability request count metric: %w", m.err)
		return
	}

	m.gatewayGlobalThrottled, m.err = meter.Int64Counter(
		"http_trigger_capability_gateway_global_throttled",
		metric.WithDescription("Number of HTTP trigger requests throttled due to global rate limit"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability gateway global throttled metric: %w", m.err)
		return
	}

	m.gatewayNodeThrottled, m.err = meter.Int64Counter(
		"http_trigger_capability_gateway_node_throttled",
		metric.WithDescription("Number of HTTP trigger requests throttled due to per-node rate limit"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability gateway node throttled metric: %w", m.err)
		return
	}

	m.requestSuccessCount, m.err = meter.Int64Counter(
		"http_trigger_capability_request_success_count",
		metric.WithDescription("Number of successful HTTP trigger responses sent to gateway"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability request success count metric: %w", m.err)
		return
	}

	m.gatewaySendError, m.err = meter.Int64Counter(
		"http_trigger_capability_gateway_send_error",
		metric.WithDescription("Number of HTTP trigger gateway send errors"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability gateway send error metric: %w", m.err)
		return
	}

	m.broadcastMetadataCount, m.err = meter.Int64Counter(
		"http_trigger_capability_broadcast_metadata_count",
		metric.WithDescription("Number of HTTP trigger broadcast metadata workflow operations"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability broadcast metadata count metric: %w", m.err)
		return
	}

	m.broadcastMetadataFailures, m.err = meter.Int64Counter(
		"http_trigger_capability_broadcast_metadata_failures",
		metric.WithDescription("Number of HTTP trigger broadcast metadata workflow failures"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability broadcast metadata failures metric: %w", m.err)
		return
	}

	m.broadcastMetadataLatency, m.err = meter.Int64Histogram(
		"http_trigger_capability_broadcast_metadata_latency_ms",
		metric.WithDescription("HTTP trigger broadcast metadata latency in milliseconds"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability broadcast metadata latency metric: %w", m.err)
		return
	}

	m.pullMetadataCount, m.err = meter.Int64Counter(
		"http_trigger_capability_pull_metadata_count",
		metric.WithDescription("Number of HTTP trigger pull metadata workflow operations"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability pull metadata count metric: %w", m.err)
		return
	}

	m.pullMetadataFailures, m.err = meter.Int64Counter(
		"http_trigger_capability_pull_metadata_failures",
		metric.WithDescription("Number of HTTP trigger pull metadata workflow failures"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability pull metadata failures metric: %w", m.err)
		return
	}

	m.pullMetadataLatency, m.err = meter.Int64Histogram(
		"http_trigger_capability_pull_metadata_latency_ms",
		metric.WithDescription("HTTP trigger pull metadata latency in milliseconds"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability pull metadata latency metric: %w", m.err)
		return
	}

	m.requestLatency, m.err = meter.Int64Histogram(
		"http_trigger_capability_request_latency_ms",
		metric.WithDescription("HTTP trigger capability request processing latency in milliseconds"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create HTTP trigger capability request latency metric: %w", m.err)
		return
	}
}

func (m *Metrics) IncrementRegisterCount(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger register count metric", "error", m.err)
		}
		return
	}
	m.registerCount.Add(ctx, 1)
}

func (m *Metrics) IncrementDeregisterCount(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger deregister count metric", "error", m.err)
		}
		return
	}
	m.deregisterCount.Add(ctx, 1)
}

func (m *Metrics) IncrementRegisterFailureCount(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger register failure count metric", "error", m.err)
		}
		return
	}
	m.registerFailureCount.Add(ctx, 1)
}

func (m *Metrics) IncrementDeregisterFailureCount(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger deregister failure count metric", "error", m.err)
		}
		return
	}
	m.deregisterFailureCount.Add(ctx, 1)
}

func (m *Metrics) IncrementRequestCacheCleanUpCount(ctx context.Context, count int64, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger request cache cleanup count metric", "error", m.err)
		}
		return
	}
	m.requestCacheCleanUpCount.Add(ctx, count)
}

func (m *Metrics) IncrementRequestCount(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability request count metric", "error", m.err)
		}
		return
	}
	m.requestCount.Add(ctx, 1)
}

func (m *Metrics) IncrementGatewayGlobalThrottled(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability gateway global throttled metric", "error", m.err)
		}
		return
	}
	m.gatewayGlobalThrottled.Add(ctx, 1)
}

func (m *Metrics) IncrementGatewayNodeThrottled(ctx context.Context, nodeAddress string, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability gateway node throttled metric", "error", m.err)
		}
		return
	}
	m.gatewayNodeThrottled.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress)))
}

func (m *Metrics) IncrementRequestSuccessCount(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability request success count metric", "error", m.err)
		}
		return
	}
	m.requestSuccessCount.Add(ctx, 1)
}

func (m *Metrics) IncrementGatewaySendError(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability gateway send error metric", "error", m.err)
		}
		return
	}
	m.gatewaySendError.Add(ctx, 1)
}

func (m *Metrics) IncrementBroadcastMetadataCount(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability broadcast metadata count metric", "error", m.err)
		}
		return
	}
	m.broadcastMetadataCount.Add(ctx, 1)
}

func (m *Metrics) IncrementBroadcastMetadataFailures(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability broadcast metadata failures metric", "error", m.err)
		}
		return
	}
	m.broadcastMetadataFailures.Add(ctx, 1)
}

func (m *Metrics) RecordBroadcastMetadataLatency(ctx context.Context, latencyMs int64, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability broadcast metadata latency metric", "error", m.err)
		}
		return
	}
	m.broadcastMetadataLatency.Record(ctx, latencyMs)
}

func (m *Metrics) IncrementPullMetadataCount(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability pull metadata count metric", "error", m.err)
		}
		return
	}
	m.pullMetadataCount.Add(ctx, 1)
}

func (m *Metrics) IncrementPullMetadataFailures(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability pull metadata failures metric", "error", m.err)
		}
		return
	}
	m.pullMetadataFailures.Add(ctx, 1)
}

func (m *Metrics) RecordPullMetadataLatency(ctx context.Context, latencyMs int64, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability pull metadata latency metric", "error", m.err)
		}
		return
	}
	m.pullMetadataLatency.Record(ctx, latencyMs)
}

func (m *Metrics) RecordRequestLatency(ctx context.Context, latencyMs int64, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP trigger capability request latency metric", "error", m.err)
		}
		return
	}
	m.requestLatency.Record(ctx, latencyMs)
}
