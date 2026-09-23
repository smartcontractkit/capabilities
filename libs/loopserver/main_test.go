package loopserver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

type stubCapabilities struct {
	initErr error
}

func (s *stubCapabilities) Start(context.Context) error    { return nil }
func (s *stubCapabilities) Close() error                   { return nil }
func (s *stubCapabilities) HealthReport() map[string]error { return nil }
func (s *stubCapabilities) Name() string                   { return "stub" }
func (s *stubCapabilities) Ready() error                   { return nil }
func (s *stubCapabilities) Initialise(context.Context, core.StandardCapabilitiesDependencies) error {
	return s.initErr
}
func (s *stubCapabilities) Infos(context.Context) ([]capabilities.CapabilityInfo, error) {
	return nil, nil
}

func useManualMetricReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previousClient := beholder.GetClient()
	client := beholder.NewNoopClient()
	client.MeterProvider = provider
	client.Meter = provider.Meter("libs-loopserver-test")
	beholder.SetClient(client)

	t.Cleanup(func() {
		beholder.SetClient(previousClient)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	return reader
}

func gaugeValue(t *testing.T, reader *sdkmetric.ManualReader, name, capability string) (int64, bool) {
	t.Helper()
	var resourceMetrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &resourceMetrics))

	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, metric := range scopeMetrics.Metrics {
			if metric.Name != name {
				continue
			}
			if gauge, ok := metric.Data.(metricdata.Gauge[int64]); ok {
				for _, dp := range gauge.DataPoints {
					capabilityVal, has := dp.Attributes.Value(attribute.String("capability", "").Key)
					if has && capabilityVal.AsString() == capability {
						return dp.Value, true
					}
				}
			}
		}
	}
	return 0, false
}

func TestInitMetrics(t *testing.T) {
	t.Run("records failure", func(t *testing.T) {
		reader := useManualMetricReader(t)
		im, err := newInitMetrics()
		require.NoError(t, err)

		im.recordInit(t.Context(), "evm", errors.New("boom"))

		failure, ok := gaugeValue(t, reader, "chain_capability_initialization_failure", "evm")
		require.True(t, ok, "failure gauge should have a data point for capability=evm")
		assert.Equal(t, int64(1), failure)

		success, ok := gaugeValue(t, reader, "chain_capability_initialization_success", "evm")
		require.True(t, ok, "success gauge should have a data point for capability=evm")
		assert.Equal(t, int64(0), success)
	})

	t.Run("records success", func(t *testing.T) {
		reader := useManualMetricReader(t)
		im, err := newInitMetrics()
		require.NoError(t, err)

		im.recordInit(t.Context(), "stellar", nil)

		success, ok := gaugeValue(t, reader, "chain_capability_initialization_success", "stellar")
		require.True(t, ok, "success gauge should have a data point for capability=stellar")
		assert.Equal(t, int64(1), success)

		failure, ok := gaugeValue(t, reader, "chain_capability_initialization_failure", "stellar")
		require.True(t, ok, "failure gauge should have a data point for capability=stellar")
		assert.Equal(t, int64(0), failure)
	})

	t.Run("nil receiver is a no-op", func(t *testing.T) {
		var im *initMetrics
		assert.NotPanics(t, func() {
			im.recordInit(t.Context(), "evm", nil)
		})
	})
}

func TestSettingsInterceptorInitialise(t *testing.T) {
	t.Run("init error is returned and recorded", func(t *testing.T) {
		reader := useManualMetricReader(t)
		im, err := newInitMetrics()
		require.NoError(t, err)
		i := &settingsInterceptor{
			StandardCapabilities: &stubCapabilities{initErr: errors.New("init failed")},
			lggr:                 logger.Nop(),
			initMetrics:          im,
		}

		require.Error(t, i.Initialise(t.Context(), core.StandardCapabilitiesDependencies{}))

		failure, ok := gaugeValue(t, reader, "chain_capability_initialization_failure", i.lggr.Name())
		require.True(t, ok)
		assert.Equal(t, int64(1), failure)
	})

	t.Run("init success is recorded", func(t *testing.T) {
		reader := useManualMetricReader(t)
		im, err := newInitMetrics()
		require.NoError(t, err)
		i := &settingsInterceptor{
			StandardCapabilities: &stubCapabilities{},
			lggr:                 logger.Nop(),
			initMetrics:          im,
		}

		require.NoError(t, i.Initialise(t.Context(), core.StandardCapabilitiesDependencies{}))

		success, ok := gaugeValue(t, reader, "chain_capability_initialization_success", i.lggr.Name())
		require.True(t, ok)
		assert.Equal(t, int64(1), success)
	})
}
