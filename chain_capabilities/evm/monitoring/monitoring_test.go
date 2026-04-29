package monitoring_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/monitoring/mocks"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
)

func TestProcessor_Process_InitiatedMessages(t *testing.T) {
	ctx := t.Context()
	initiated := []struct {
		name string
		msg  proto.Message
	}{
		{"CallContractInitiated", &monitoring.CallContractInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}, Req: &monitoring.CallContractRequest{}}},
		{"WriteReportInitiated", &monitoring.WriteReportInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"LogTriggerInitiated", &monitoring.LogTriggerInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"FilterLogsInitiated", &monitoring.FilterLogsInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"BalanceAtInitiated", &monitoring.BalanceAtInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"EstimateGasInitiated", &monitoring.EstimateGasInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionByHashInitiated", &monitoring.GetTransactionByHashInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionReceiptInitiated", &monitoring.GetTransactionReceiptInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"HeaderByNumberInitiated", &monitoring.HeaderByNumberInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
	}

	for _, tc := range initiated {
		t.Run(tc.name, func(t *testing.T) {
			lggr := logger.Test(t)

			metrics, merr := monitoring.NewMetrics()
			require.NoError(t, merr)

			p, err := monitoring.NewProcessor(lggr, metrics)
			require.NoError(t, err)

			require.NoError(t, p.Process(ctx, tc.msg))
		})
	}
}

type dummyProto struct{}

func (d *dummyProto) ProtoReflect() protoreflect.Message {
	return nil
}

func TestProcessor_Process_UnknownMessage_Noop(t *testing.T) {
	lggr := logger.Test(t)
	metrics, merr := monitoring.NewMetrics()
	require.NoError(t, merr)

	p, err := monitoring.NewProcessor(lggr, metrics)
	require.NoError(t, err)

	err = p.Process(t.Context(), &dummyProto{})
	require.NoError(t, err)
}

func TestProcessor_Process_SuccessMessages(t *testing.T) {
	ctx := t.Context()
	successMsgs := []struct {
		name string
		msg  proto.Message
	}{
		{"CallContractSuccess", &monitoring.CallContractSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"WriteReportSuccess", &monitoring.WriteReportSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"LogTriggerSuccess", &monitoring.LogTriggerSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"FilterLogsSuccess", &monitoring.FilterLogsSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"BalanceAtSuccess", &monitoring.BalanceAtSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"EstimateGasSuccess", &monitoring.EstimateGasSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionByHashSuccess", &monitoring.GetTransactionByHashSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionReceiptSuccess", &monitoring.GetTransactionReceiptSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"HeaderByNumberSuccess", &monitoring.HeaderByNumberSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
	}

	for _, tc := range successMsgs {
		t.Run(tc.name, func(t *testing.T) {
			lggr := logger.Test(t)

			metrics, merr := monitoring.NewMetrics()
			require.NoError(t, merr)

			p, err := monitoring.NewProcessor(lggr, metrics)
			require.NoError(t, err)

			require.NoError(t, p.Process(ctx, tc.msg))
		})
	}
}

func TestProcessor_Process_ErrorMessages(t *testing.T) {
	ctx := t.Context()
	errorMsgs := []struct {
		name string
		msg  proto.Message
	}{
		{"CallContractError", &monitoring.CallContractError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"WriteReportError", &monitoring.WriteReportError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"WriteReportUserError", &monitoring.WriteReportError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"LogTriggerError", &monitoring.LogTriggerError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"LogTriggerCleanUpError", &monitoring.LogTriggerCleanUpError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"LogTriggerEventDroppedError", &monitoring.LogTriggerEventDroppedError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"FilterLogsError", &monitoring.FilterLogsError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"BalanceAtError", &monitoring.BalanceAtError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"EstimateGasError", &monitoring.EstimateGasError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionByHashError", &monitoring.GetTransactionByHashError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionReceiptError", &monitoring.GetTransactionReceiptError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"HeaderByNumberError", &monitoring.HeaderByNumberError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
	}

	for _, tc := range errorMsgs {
		t.Run(tc.name, func(t *testing.T) {
			lggr := logger.Test(t)

			metrics, merr := monitoring.NewMetrics()
			require.NoError(t, merr)

			p, err := monitoring.NewProcessor(lggr, metrics)
			require.NoError(t, err)

			require.NoError(t, p.Process(ctx, tc.msg))
		})
	}
}

func TestMetrics_ReadActionsEmitLegacyAndGenericMetrics(t *testing.T) {
	ctx := t.Context()
	reader := useManualMetricReader(t)

	metrics, err := monitoring.NewMetrics()
	require.NoError(t, err)

	executionContext := &capmonitoring.ExecutionContext{
		MetaChainFamilyName:          "evm",
		MetaChainId:                  "1",
		MetaNetworkName:              "mainnet",
		MetaNetworkNameFull:          "ethereum-mainnet",
		MetaWorkflowDonId:            7,
		MetaCapabilityType:           "action",
		MetaCapabilityId:             "evm",
		MetaCapabilityTimestampStart: 100,
		MetaCapabilityTimestampEmit:  125,
		MetaWorkflowDonConfigVersion: 11,
		MetaWorkflowExecutionId:      "workflow-execution",
		MetaReferenceId:              "read",
	}

	testCases := []struct {
		action        string
		recordSuccess func() error
		recordError   func() error
	}{
		{
			action: "call_contract",
			recordSuccess: func() error {
				return metrics.OnCallContractSuccess(ctx, &monitoring.CallContractSuccess{ExecutionContext: executionContext})
			},
			recordError: func() error {
				return metrics.OnCallContractError(ctx, &monitoring.CallContractError{ExecutionContext: executionContext})
			},
		},
		{
			action: "filter_logs",
			recordSuccess: func() error {
				return metrics.OnFilterLogsSuccess(ctx, &monitoring.FilterLogsSuccess{ExecutionContext: executionContext})
			},
			recordError: func() error {
				return metrics.OnFilterLogsError(ctx, &monitoring.FilterLogsError{ExecutionContext: executionContext})
			},
		},
		{
			action: "balance_at",
			recordSuccess: func() error {
				return metrics.OnBalanceAtSuccess(ctx, &monitoring.BalanceAtSuccess{ExecutionContext: executionContext})
			},
			recordError: func() error {
				return metrics.OnBalanceAtError(ctx, &monitoring.BalanceAtError{ExecutionContext: executionContext})
			},
		},
		{
			action: "estimate_gas",
			recordSuccess: func() error {
				return metrics.OnEstimateGasSuccess(ctx, &monitoring.EstimateGasSuccess{ExecutionContext: executionContext})
			},
			recordError: func() error {
				return metrics.OnEstimateGasError(ctx, &monitoring.EstimateGasError{ExecutionContext: executionContext})
			},
		},
		{
			action: "get_transaction_by_hash",
			recordSuccess: func() error {
				return metrics.OnGetTransactionByHashSuccess(ctx, &monitoring.GetTransactionByHashSuccess{ExecutionContext: executionContext})
			},
			recordError: func() error {
				return metrics.OnGetTransactionByHashError(ctx, &monitoring.GetTransactionByHashError{ExecutionContext: executionContext})
			},
		},
		{
			action: "get_transaction_receipt",
			recordSuccess: func() error {
				return metrics.OnGetTransactionReceiptSuccess(ctx, &monitoring.GetTransactionReceiptSuccess{ExecutionContext: executionContext})
			},
			recordError: func() error {
				return metrics.OnGetTransactionReceiptError(ctx, &monitoring.GetTransactionReceiptError{ExecutionContext: executionContext})
			},
		},
		{
			action: "header_by_number",
			recordSuccess: func() error {
				return metrics.OnHeaderByNumberSuccess(ctx, &monitoring.HeaderByNumberSuccess{ExecutionContext: executionContext})
			},
			recordError: func() error {
				return metrics.OnHeaderByNumberError(ctx, &monitoring.HeaderByNumberError{ExecutionContext: executionContext})
			},
		},
	}

	for _, tc := range testCases {
		require.NoError(t, tc.recordSuccess())
		require.NoError(t, tc.recordError())
	}

	var resourceMetrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &resourceMetrics))

	for _, tc := range testCases {
		legacySuccessPrefix := fmt.Sprintf("evm_capability_%s_success", tc.action)
		legacyErrorPrefix := fmt.Sprintf("evm_capability_%s_error", tc.action)

		require.True(t, metricExists(resourceMetrics, legacySuccessPrefix+"_count", metricExpectation{
			required: map[string]string{"chain_id": "1"},
			absent:   []attribute.Key{"action"},
		}), "missing unchanged legacy success count metric for %s", tc.action)
		require.True(t, metricExists(resourceMetrics, legacyErrorPrefix+"_count", metricExpectation{
			required: map[string]string{"chain_id": "1"},
			absent:   []attribute.Key{"action"},
		}), "missing unchanged legacy error count metric for %s", tc.action)

		require.True(t, metricExists(resourceMetrics, "evm_capability_read_success_count", metricExpectation{
			required: map[string]string{"chain_id": "1", "action": tc.action},
		}), "missing generic success count metric for %s", tc.action)
		require.True(t, metricExists(resourceMetrics, "evm_capability_read_error_count", metricExpectation{
			required: map[string]string{"chain_id": "1", "action": tc.action},
		}), "missing generic error count metric for %s", tc.action)

		require.True(t, metricExists(resourceMetrics, legacySuccessPrefix+"_cap_duration", metricExpectation{
			required: map[string]string{"chain_id": "1"},
			absent:   []attribute.Key{"action"},
		}), "missing unchanged legacy success duration metric for %s", tc.action)
		require.True(t, metricExists(resourceMetrics, legacyErrorPrefix+"_cap_duration", metricExpectation{
			required: map[string]string{"chain_id": "1"},
			absent:   []attribute.Key{"action"},
		}), "missing unchanged legacy error duration metric for %s", tc.action)

		require.True(t, metricExists(resourceMetrics, "evm_capability_read_success_cap_duration", metricExpectation{
			required: map[string]string{"chain_id": "1", "action": tc.action},
		}), "missing generic success duration metric for %s", tc.action)
		require.True(t, metricExists(resourceMetrics, "evm_capability_read_error_cap_duration", metricExpectation{
			required: map[string]string{"chain_id": "1", "action": tc.action},
		}), "missing generic error duration metric for %s", tc.action)
	}
}

func TestMetricViews_ReadLatencyBuckets(t *testing.T) {
	views := monitoring.MetricViews()
	expectedBuckets := []float64{
		0, 5, 10, 25, 50, 75, 100,
		250, 500, 750, 1000,
		2500, 5000, 7500, 10000,
		15000, 30000,
	}
	expectedMetricNames := []string{
		"evm_capability_read_success_cap_duration",
		"evm_capability_read_error_cap_duration",
		"evm_capability_call_contract_success_cap_duration",
		"evm_capability_call_contract_error_cap_duration",
		"evm_capability_filter_logs_success_cap_duration",
		"evm_capability_filter_logs_error_cap_duration",
		"evm_capability_balance_at_success_cap_duration",
		"evm_capability_balance_at_error_cap_duration",
		"evm_capability_estimate_gas_success_cap_duration",
		"evm_capability_estimate_gas_error_cap_duration",
		"evm_capability_get_transaction_by_hash_success_cap_duration",
		"evm_capability_get_transaction_by_hash_error_cap_duration",
		"evm_capability_get_transaction_receipt_success_cap_duration",
		"evm_capability_get_transaction_receipt_error_cap_duration",
		"evm_capability_header_by_number_success_cap_duration",
		"evm_capability_header_by_number_error_cap_duration",
	}

	require.Len(t, views, len(expectedMetricNames))
	for _, name := range expectedMetricNames {
		stream, ok := metricViewStream(views, name)
		require.True(t, ok, "missing metric view for %s", name)

		aggregation, ok := stream.Aggregation.(sdkmetric.AggregationExplicitBucketHistogram)
		require.True(t, ok, "expected explicit bucket histogram for %s", name)
		require.Equal(t, expectedBuckets, aggregation.Boundaries)
	}

	_, ok := metricViewStream(views, "evm_capability_write_report_success_cap_duration")
	require.False(t, ok, "write report latency should keep the default buckets")
}

func metricViewStream(views []sdkmetric.View, name string) (sdkmetric.Stream, bool) {
	for _, view := range views {
		if stream, ok := view(sdkmetric.Instrument{Name: name}); ok {
			return stream, true
		}
	}
	return sdkmetric.Stream{}, false
}

func useManualMetricReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previousClient := beholder.GetClient()
	client := beholder.NewNoopClient()
	client.MeterProvider = provider
	client.Meter = provider.Meter("evm-monitoring-test")
	beholder.SetClient(client)

	t.Cleanup(func() {
		beholder.SetClient(previousClient)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	return reader
}

type metricExpectation struct {
	required map[string]string
	absent   []attribute.Key
}

func metricExists(resourceMetrics metricdata.ResourceMetrics, name string, expectation metricExpectation) bool {
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, metric := range scopeMetrics.Metrics {
			if metric.Name != name {
				continue
			}
			for _, attrs := range metricAttributeSets(metric) {
				if attributesMatch(attrs, expectation) {
					return true
				}
			}
		}
	}
	return false
}

func metricAttributeSets(metric metricdata.Metrics) []attribute.Set {
	switch data := metric.Data.(type) {
	case metricdata.Sum[int64]:
		sets := make([]attribute.Set, 0, len(data.DataPoints))
		for _, dp := range data.DataPoints {
			sets = append(sets, dp.Attributes)
		}
		return sets
	case metricdata.Gauge[int64]:
		sets := make([]attribute.Set, 0, len(data.DataPoints))
		for _, dp := range data.DataPoints {
			sets = append(sets, dp.Attributes)
		}
		return sets
	case metricdata.Histogram[int64]:
		sets := make([]attribute.Set, 0, len(data.DataPoints))
		for _, dp := range data.DataPoints {
			sets = append(sets, dp.Attributes)
		}
		return sets
	default:
		return nil
	}
}

func attributesMatch(attrs attribute.Set, expectation metricExpectation) bool {
	for key, expected := range expectation.required {
		actual, ok := attrs.Value(attribute.Key(key))
		if !ok || actual.AsString() != expected {
			return false
		}
	}
	for _, key := range expectation.absent {
		if attrs.HasValue(key) {
			return false
		}
	}
	return true
}

type ProcessorMock struct {
	mock.Mock
}

func (m *ProcessorMock) Process(ctx context.Context, pm proto.Message, _ ...any) error {
	args := m.Called(ctx, pm)
	return args.Error(0)
}

type stubSuccessMessage struct{ *emptypb.Empty }

func (s *stubSuccessMessage) LogAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("foo", "bar")}
}

func (s *stubSuccessMessage) MetricAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("foo_metric", "bar_metric")}
}

// stubErrorMessage implements monitoring.ErrorMessage via embedded Empty
// and GetSummary/GetCause.
type stubErrorMessage struct{ *emptypb.Empty }

func (s *stubErrorMessage) LogAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("key", "val"), attribute.String("summary", "summaryString")}
}

func (s *stubErrorMessage) MetricAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("key", "val")}
}

func (s *stubErrorMessage) GetSummary() string { return "summary" }
func (s *stubErrorMessage) GetCause() string   { return "cause" }

func TestLogEmit_SuccessCases(t *testing.T) {
	cases := []struct {
		name    string
		success string
		err     error
	}{
		{"NoError", "operation succeeded", nil},
		{"WithError", "operation succeeded", errors.New("boom")},
	}

	ctx := t.Context()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logger := mocks.NewLogger(t)
			proc := new(ProcessorMock)
			msg := &stubSuccessMessage{Empty: &emptypb.Empty{}}

			logger.EXPECT().Infow(c.success, "foo", "bar").Once()
			proc.On("Process", ctx, msg).Return(c.err).Once()
			if c.err != nil {
				logger.EXPECT().Errorw("Failed to process Empty message", "err", c.err).Once()
			}

			monitoring.LogAndEmitSuccess(ctx, c.success, logger, proc, msg)
			proc.AssertExpectations(t)
		})
	}
}

func TestLogEmit_ErrorCases(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"NoError", nil},
		{"WithError", errors.New("boom")},
	}

	ctx := t.Context()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logger := mocks.NewLogger(t)
			proc := new(ProcessorMock)
			msg := &stubErrorMessage{Empty: &emptypb.Empty{}}

			logger.EXPECT().Errorw("summary err: cause", "key", "val").Once()
			proc.On("Process", ctx, msg).Return(c.err).Once()

			if c.err != nil {
				logger.EXPECT().Errorw("Failed to process Empty message", "err", c.err).Once()
			}

			monitoring.LogAndEmitError(ctx, logger, proc, msg)
			proc.AssertExpectations(t)
		})
	}
}
