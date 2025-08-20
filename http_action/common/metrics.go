package common

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const (
	AttrNodeAddress = "node_address"
)

// Metrics contains metrics for HTTP actions
type Metrics struct {
	requestCount            metric.Int64Counter
	inputValidationFailures metric.Int64Counter
	workflowOwnerThrottled  metric.Int64Counter
	nodeThrottled           metric.Int64Counter
	gatewayConnectionError  metric.Int64Counter
	gatewaySendError        metric.Int64Counter
	successfulResponse      metric.Int64Counter
	executionError          metric.Int64Counter
	gatewayNodeThrottled    metric.Int64Counter
	gatewayGlobalThrottled  metric.Int64Counter
	requestLatency          metric.Int64Histogram

	err error
}

// NewMetrics creates a new instance of Metrics
func NewMetrics() *Metrics {
	m := &Metrics{}
	m.init()
	return m
}

func (m *Metrics) init() {
	meter := beholder.GetMeter()

	m.requestCount, m.err = meter.Int64Counter(
		"http_action_request_count",
		metric.WithDescription("Number of HTTP action requests"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create request count metric: %w", m.err)
		return
	}

	m.inputValidationFailures, m.err = meter.Int64Counter(
		"http_action_validation_failure_count",
		metric.WithDescription("Number of HTTP action input validation failures"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create validation failure metric: %w", m.err)
		return
	}

	m.workflowOwnerThrottled, m.err = meter.Int64Counter(
		"http_action_workflow_owner_throttled_count",
		metric.WithDescription("Number of HTTP action requests exceeding per-workflow-owner rate limit"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create workflow owner throttled metric: %w", m.err)
		return
	}

	m.nodeThrottled, m.err = meter.Int64Counter(
		"http_action_node_throttled_count",
		metric.WithDescription("Number of HTTP action requests exceeding global rate limit"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create node throttled metric: %w", m.err)
		return
	}

	m.gatewayConnectionError, m.err = meter.Int64Counter(
		"http_action_capability_gateway_connection_error_count",
		metric.WithDescription("Number of HTTP action gateway connection errors"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create capability gateway connection error metric: %w", m.err)
		return
	}

	m.gatewaySendError, m.err = meter.Int64Counter(
		"http_action_capability_gateway_send_error_count",
		metric.WithDescription("Number of HTTP action gateway send errors"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create capability gateway send error metric: %w", m.err)
		return
	}

	m.successfulResponse, m.err = meter.Int64Counter(
		"http_action_successful_response_count",
		metric.WithDescription("Number of HTTP action successful responses"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create successful response metric: %w", m.err)
		return
	}

	m.executionError, m.err = meter.Int64Counter(
		"http_action_execution_error_count",
		metric.WithDescription("Number of HTTP action execution errors"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create execution error metric: %w", m.err)
		return
	}

	m.gatewayNodeThrottled, m.err = meter.Int64Counter(
		"http_action_capability_gateway_node_throttled_count",
		metric.WithDescription("Number of throttled requests while receiving HTTP action response from gateway. Per-gateway-node rate limit"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create capability gateway node throttled metric: %w", m.err)
		return
	}

	m.gatewayGlobalThrottled, m.err = meter.Int64Counter(
		"http_action_capability_gateway_global_throttled_count",
		metric.WithDescription("Number of throttled requests while receiving HTTP action response from gateway. Global limit"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create capability gateway global throttled metric: %w", m.err)
		return
	}

	m.requestLatency, m.err = meter.Int64Histogram(
		"http_action_request_latency_ms",
		metric.WithDescription("HTTP action request latency in milliseconds"),
	)
	if m.err != nil {
		m.err = fmt.Errorf("failed to create request latency metric: %w", m.err)
		return
	}
}

func (m *Metrics) IncrementRequestCount(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP action request count metric", "error", m.err)
		}
		return
	}
	m.requestCount.Add(ctx, 1)
}

func (m *Metrics) IncrementInputValidationFailures(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP action validation failure metric", "error", m.err)
		}
		return
	}
	m.inputValidationFailures.Add(ctx, 1)
}

func (m *Metrics) IncrementWorkflowOwnerThrottled(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP action workflow owner throttled metric", "error", m.err)
		}
		return
	}
	m.workflowOwnerThrottled.Add(ctx, 1)
}

func (m *Metrics) IncrementNodeThrottled(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP action node throttled metric", "error", m.err)
		}
		return
	}
	m.nodeThrottled.Add(ctx, 1)
}

func (m *Metrics) IncrementGatewayConnectionError(ctx context.Context, nodeAddress string, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP action capability gateway connection error metric", "error", m.err)
		}
		return
	}
	m.gatewayConnectionError.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress)))
}

func (m *Metrics) IncrementGatewaySendError(ctx context.Context, nodeAddress string, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP action capability gateway send error metric", "error", m.err)
		}
		return
	}
	m.gatewaySendError.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress)))
}

func (m *Metrics) IncrementSuccessfulResponse(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP action successful response metric", "error", m.err)
		}
		return
	}
	m.successfulResponse.Add(ctx, 1)
}

func (m *Metrics) IncrementExecutionError(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP action internal execution error metric. Any errors that happen before or after receiving a response from the customer's endpoint", "error", m.err)
		}
		return
	}
	m.executionError.Add(ctx, 1)
}

func (m *Metrics) IncrementGatewayNodeThrottled(ctx context.Context, nodeAddress string, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP action capability gateway node throttled metric", "error", m.err)
		}
		return
	}
	m.gatewayNodeThrottled.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress)))
}

func (m *Metrics) IncrementGatewayGlobalThrottled(ctx context.Context, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP action capability gateway global throttled metric", "error", m.err)
		}
		return
	}
	m.gatewayGlobalThrottled.Add(ctx, 1)
}

func (m *Metrics) RecordRequestLatency(ctx context.Context, latencyMs int64, lggr logger.Logger) {
	if m.err != nil {
		if lggr != nil {
			lggr.Errorw("Failed to initialize HTTP action request latency metric", "error", m.err)
		}
		return
	}
	m.requestLatency.Record(ctx, latencyMs)
}
