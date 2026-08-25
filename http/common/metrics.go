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
	AttrProxyMode   = "proxy_mode"
	AttrStatusCode  = "status_code"
	AttrMethodName  = "method_name"
	AttrSuccess     = "success"
)

// Metrics contains metrics for HTTP actions
type Metrics struct {
	requestCount                    metric.Int64Counter
	inputValidationFailures         metric.Int64Counter
	gatewaySendError                metric.Int64Counter
	gatewaySendCount                metric.Int64Counter
	successfulResponse              metric.Int64Counter
	executionError                  metric.Int64Counter
	executionTimeout                metric.Int64Counter
	externalEndpointError           metric.Int64Counter
	requestLatency                  metric.Int64Histogram
	requestLatencyExcludingExternal metric.Int64Histogram
	externalEndpointLatency         metric.Int64Histogram
}

// NewMetrics creates a new instance of Metrics
func NewMetrics() (*Metrics, error) {
	m := &Metrics{}
	if err := m.init(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Metrics) init() error {
	meter := beholder.GetMeter()
	var err error

	m.requestCount, err = meter.Int64Counter(
		"http_action_request_count",
		metric.WithDescription("Number of HTTP action requests"),
	)
	if err != nil {
		return fmt.Errorf("failed to create request count metric: %w", err)
	}

	m.inputValidationFailures, err = meter.Int64Counter(
		"http_action_validation_failure_count",
		metric.WithDescription("Number of HTTP action input validation failures"),
	)
	if err != nil {
		return fmt.Errorf("failed to create validation failure metric: %w", err)
	}

	m.gatewaySendError, err = meter.Int64Counter(
		"http_action_capability_gateway_send_error_count",
		metric.WithDescription("Number of HTTP action gateway send and connection errors"),
	)
	if err != nil {
		return fmt.Errorf("failed to create capability gateway send error metric: %w", err)
	}

	m.gatewaySendCount, err = meter.Int64Counter(
		"http_action_capability_gateway_send_count",
		metric.WithDescription("Number of HTTP action requests sent to gateway"),
	)
	if err != nil {
		return fmt.Errorf("failed to create capability gateway send count metric: %w", err)
	}

	m.successfulResponse, err = meter.Int64Counter(
		"http_action_successful_response_count",
		metric.WithDescription("Number of HTTP action successful responses"),
	)
	if err != nil {
		return fmt.Errorf("failed to create successful response metric: %w", err)
	}

	m.executionError, err = meter.Int64Counter(
		"http_action_execution_error_count",
		metric.WithDescription("Number of HTTP action execution errors"),
	)
	if err != nil {
		return fmt.Errorf("failed to create execution error metric: %w", err)
	}

	m.executionTimeout, err = meter.Int64Counter(
		"http_action_execution_timeout_count",
		metric.WithDescription("Number of HTTP action execution timeouts"),
	)
	if err != nil {
		return fmt.Errorf("failed to create execution timeout metric: %w", err)
	}

	m.externalEndpointError, err = meter.Int64Counter(
		"http_action_external_endpoint_error_count",
		metric.WithDescription("Number of HTTP action external endpoint errors"),
	)
	if err != nil {
		return fmt.Errorf("failed to create external endpoint error metric: %w", err)
	}

	m.requestLatency, err = meter.Int64Histogram(
		"http_action_request_latency_ms",
		metric.WithDescription("HTTP action request latency in milliseconds"),
	)
	if err != nil {
		return fmt.Errorf("failed to create request latency metric: %w", err)
	}

	m.requestLatencyExcludingExternal, err = meter.Int64Histogram(
		"http_action_request_latency_ms_excluding_external_endpoint",
		metric.WithDescription("HTTP action request latency in milliseconds excluding external endpoint call time"),
	)
	if err != nil {
		return fmt.Errorf("failed to create request latency excluding external metric: %w", err)
	}

	m.externalEndpointLatency, err = meter.Int64Histogram(
		"http_action_external_endpoint_latency_ms",
		metric.WithDescription("HTTP action external endpoint latency in milliseconds"),
	)
	if err != nil {
		return fmt.Errorf("failed to create external endpoint latency metric: %w", err)
	}

	return nil
}

func (m *Metrics) IncrementRequestCount(ctx context.Context, lggr logger.Logger) {
	m.requestCount.Add(ctx, 1)
}

func (m *Metrics) IncrementInputValidationFailures(ctx context.Context, lggr logger.Logger) {
	m.inputValidationFailures.Add(ctx, 1)
}

func (m *Metrics) IncrementGatewaySendError(ctx context.Context, nodeAddress string, lggr logger.Logger) {
	m.executionError.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrProxyMode, ProxyModeGateway.String())))
	m.gatewaySendError.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress)))
}

func (m *Metrics) IncrementSuccessfulResponse(ctx context.Context, proxyMode ProxyMode, statusCode uint32, lggr logger.Logger) {
	m.successfulResponse.Add(ctx, 1, metric.WithAttributes(
		attribute.String(AttrProxyMode, proxyMode.String()),
		attribute.Int64(AttrStatusCode, int64(statusCode))))
}

func (m *Metrics) IncrementExecutionError(ctx context.Context, proxyMode ProxyMode, lggr logger.Logger) {
	m.executionError.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrProxyMode, proxyMode.String())))
}

func (m *Metrics) IncrementExecutionTimeout(ctx context.Context, proxyMode ProxyMode, lggr logger.Logger) {
	m.executionTimeout.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrProxyMode, proxyMode.String())))
	m.executionError.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrProxyMode, proxyMode.String())))
}

func (m *Metrics) IncrementExternalEndpointError(ctx context.Context, proxyMode ProxyMode, lggr logger.Logger) {
	m.externalEndpointError.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrProxyMode, proxyMode.String())))
}

func (m *Metrics) IncrementGatewaySendCount(ctx context.Context, nodeAddress string, lggr logger.Logger) {
	m.gatewaySendCount.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress)))
}

func (m *Metrics) RecordRequestLatency(ctx context.Context, totalLatencyMs, externalLatencyMs int64, proxyMode ProxyMode, success bool, lggr logger.Logger) {
	attrs := metric.WithAttributes(
		attribute.String(AttrProxyMode, proxyMode.String()),
		attribute.Bool(AttrSuccess, success),
	)
	latencyMsExcludingExternal := totalLatencyMs - externalLatencyMs
	m.requestLatency.Record(ctx, totalLatencyMs, attrs)
	m.requestLatencyExcludingExternal.Record(ctx, latencyMsExcludingExternal, attrs)
	m.externalEndpointLatency.Record(ctx, externalLatencyMs, attrs)
}
