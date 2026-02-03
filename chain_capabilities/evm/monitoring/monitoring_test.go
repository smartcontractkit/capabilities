package monitoring_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/monitoring/mocks"

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
