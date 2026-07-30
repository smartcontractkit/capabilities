package main

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

// Attribute values for the endpoint kind and message direction on proxy metrics.
const (
	endpointOCR2      = "ocr2"
	endpointOCR3_1    = "ocr3_1"
	endpointPeerGroup = "peergroup"

	directionInbound  = "inbound"
	directionOutbound = "outbound"
)

// proxyMetrics holds the otel instruments for the p2p proxy.
type proxyMetrics struct {
	messageSize metric.Int64Histogram
}

// newProxyMetrics creates the proxy instruments. It must be called after the
// bootstrapper has started (which configures the beholder client, including the
// views from metricViews), or the instruments bind to the noop meter forever.
func newProxyMetrics() (*proxyMetrics, error) {
	messageSize, err := beholder.GetMeter().Int64Histogram(
		"p2p_proxy_message_size_bytes",
		metric.WithDescription("Size in bytes of message payloads relayed by the p2p proxy"),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create message size histogram: %w", err)
	}
	return &proxyMetrics{messageSize: messageSize}, nil
}

// sizeRecorder records payload sizes for one endpoint kind and direction, with
// the attribute set precomputed so the per-message path does not allocate.
type sizeRecorder struct {
	h     metric.Int64Histogram
	attrs metric.MeasurementOption
}

func (m *proxyMetrics) sizes(endpoint, direction string) sizeRecorder {
	return sizeRecorder{h: m.messageSize, attrs: metric.WithAttributeSet(attribute.NewSet(
		attribute.String("endpoint", endpoint),
		attribute.String("direction", direction),
	))}
}

func (r sizeRecorder) record(ctx context.Context, sizeBytes int) {
	r.h.Record(ctx, int64(sizeBytes), r.attrs)
}

// metricViews returns the otel views for the proxy's instruments. Histogram
// bucket boundaries can only be set via views when the beholder client is
// created, so these are passed to the bootstrapper via standalone.WithOtelViews.
func metricViews() []sdkmetric.View {
	return []sdkmetric.View{
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: "p2p_proxy_message_size_bytes"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				// Payloads range from tiny heartbeats to multi-MB reports; the
				// default otel buckets top out at 10k and would flatten that.
				Boundaries: []float64{0, 100, 1_000, 10_000, 100_000, 1_000_000, 10_000_000},
			}},
		),
	}
}
