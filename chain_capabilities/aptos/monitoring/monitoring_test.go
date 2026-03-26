package monitoring_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/internal/monitoring/mocks"
	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/monitoring"
)

func newTestProcessor(t *testing.T) (*mocks.ProtoEmitter, monitoring.Metrics, beholder.ProtoProcessor) {
	t.Helper()
	emitter := mocks.NewProtoEmitter(t)
	emitter.EXPECT().EmitWithLog(mock.Anything, mock.Anything).Return(nil).Maybe()
	metrics, err := monitoring.NewMetrics()
	require.NoError(t, err)
	p, err := monitoring.NewProcessor(emitter, metrics)
	require.NoError(t, err)
	return emitter, metrics, p
}

func TestProcessor_Process_InitiatedMessage(t *testing.T) {
	_, _, p := newTestProcessor(t)
	msg := &monitoring.WriteReportInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}
	require.NoError(t, p.Process(t.Context(), msg))
}

func TestProcessor_Process_SuccessMessages(t *testing.T) {
	ctx := t.Context()
	ec := &capmonitoring.ExecutionContext{}

	successMsgs := []struct {
		name string
		msg  proto.Message
	}{
		{"WriteReportSuccess", &monitoring.WriteReportSuccess{ExecutionContext: ec}},
		{"WriteReportDuplicateTx", &monitoring.WriteReportDuplicateTx{ExecutionContext: ec}},
		{"WriteReportSuccessfulEarlyReturn", &monitoring.WriteReportSuccessfulEarlyReturn{ExecutionContext: ec}},
	}

	for _, tc := range successMsgs {
		t.Run(tc.name, func(t *testing.T) {
			_, _, p := newTestProcessor(t)
			require.NoError(t, p.Process(ctx, tc.msg))
		})
	}
}

func TestProcessor_Process_ErrorMessages(t *testing.T) {
	ctx := t.Context()
	ec := &capmonitoring.ExecutionContext{}

	errorMsgs := []struct {
		name string
		msg  proto.Message
	}{
		{"WriteReportError", &monitoring.WriteReportError{ExecutionContext: ec}},
		{"WriteReportTxFeeCalculationError", &monitoring.WriteReportTxFeeCalculationError{ExecutionContext: ec}},
	}

	for _, tc := range errorMsgs {
		t.Run(tc.name, func(t *testing.T) {
			_, _, p := newTestProcessor(t)
			require.NoError(t, p.Process(ctx, tc.msg))
		})
	}
}

func TestProcessor_Process_WriteReportError_UserError_SkipsMetrics(t *testing.T) {
	_, _, p := newTestProcessor(t)
	msg := &monitoring.WriteReportError{
		ExecutionContext: &capmonitoring.ExecutionContext{},
		IsUserError:     true,
		Summary:         "user did something wrong",
		Cause:           "invalid input",
	}
	require.NoError(t, p.Process(t.Context(), msg))
}

type dummyProto struct{}

func (d *dummyProto) ProtoReflect() protoreflect.Message { return nil }

func TestProcessor_Process_UnknownMessage_Noop(t *testing.T) {
	_, _, p := newTestProcessor(t)
	err := p.Process(t.Context(), &dummyProto{})
	require.NoError(t, err)
}

func TestProcessor_Process_EmitError_Propagates(t *testing.T) {
	msgs := []struct {
		name string
		msg  proto.Message
	}{
		{"WriteReportInitiated", &monitoring.WriteReportInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"WriteReportSuccess", &monitoring.WriteReportSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"WriteReportError", &monitoring.WriteReportError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"WriteReportTxFeeCalculationError", &monitoring.WriteReportTxFeeCalculationError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"WriteReportDuplicateTx", &monitoring.WriteReportDuplicateTx{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"WriteReportSuccessfulEarlyReturn", &monitoring.WriteReportSuccessfulEarlyReturn{ExecutionContext: &capmonitoring.ExecutionContext{}}},
	}

	for _, tc := range msgs {
		t.Run(tc.name, func(t *testing.T) {
			emitter := mocks.NewProtoEmitter(t)
			emitter.EXPECT().EmitWithLog(mock.Anything, mock.Anything).Return(assert.AnError).Once()

			metrics, err := monitoring.NewMetrics()
			require.NoError(t, err)
			p, err := monitoring.NewProcessor(emitter, metrics)
			require.NoError(t, err)

			err = p.Process(t.Context(), tc.msg)
			assert.ErrorIs(t, err, assert.AnError)
		})
	}
}

func TestNewMetrics(t *testing.T) {
	metrics, err := monitoring.NewMetrics()
	require.NoError(t, err)
	assert.NotNil(t, metrics)
}
