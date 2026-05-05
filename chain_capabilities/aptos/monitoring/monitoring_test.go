package monitoring_test

import (
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/monitoring"
)

func newTestProcessor(t *testing.T) (monitoring.Metrics, beholder.ProtoProcessor) {
	t.Helper()

	lggr := logger.Test(t)
	metrics, err := monitoring.NewMetrics()
	require.NoError(t, err)
	p, err := monitoring.NewProcessor(lggr, metrics)
	require.NoError(t, err)
	return metrics, p
}

func TestProcessor_Process_InitiatedMessage(t *testing.T) {
	ctx := t.Context()
	ec := &capmonitoring.ExecutionContext{}

	initiatedMsgs := []struct {
		name string
		msg  proto.Message
	}{
		{"ViewInitiated", &monitoring.ViewInitiated{ExecutionContext: ec}},
		{"WriteReportInitiated", &monitoring.WriteReportInitiated{ExecutionContext: ec}},
	}

	for _, tc := range initiatedMsgs {
		t.Run(tc.name, func(t *testing.T) {
			_, p := newTestProcessor(t)
			require.NoError(t, p.Process(ctx, tc.msg))
		})
	}
}

func TestProcessor_Process_SuccessMessages(t *testing.T) {
	ctx := t.Context()
	ec := &capmonitoring.ExecutionContext{}

	successMsgs := []struct {
		name string
		msg  proto.Message
	}{
		{"ViewSuccess", &monitoring.ViewSuccess{ExecutionContext: ec}},
		{"WriteReportSuccess", &monitoring.WriteReportSuccess{ExecutionContext: ec}},
		{"WriteReportDuplicateTx", &monitoring.WriteReportDuplicateTx{ExecutionContext: ec}},
		{"WriteReportSuccessfulEarlyReturn", &monitoring.WriteReportSuccessfulEarlyReturn{ExecutionContext: ec}},
		{"WriteReportTxInfoRetrievalPhase", &monitoring.WriteReportTxInfoRetrievalPhase{ExecutionContext: ec, LookupType: "SuccessfulTransmission", Phase: "LastPagePoll", Result: "Found"}},
		{"WriteReportInvokeOnReportDuration", &monitoring.WriteReportInvokeOnReportDuration{ExecutionContext: ec, DurationMs: 123, TxStatus: 2}},
	}

	for _, tc := range successMsgs {
		t.Run(tc.name, func(t *testing.T) {
			_, p := newTestProcessor(t)
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
		{"ViewError", &monitoring.ViewError{ExecutionContext: ec}},
		{"WriteReportError", &monitoring.WriteReportError{ExecutionContext: ec}},
		{"WriteReportTxFeeCalculationError", &monitoring.WriteReportTxFeeCalculationError{ExecutionContext: ec}},
		{"WriteReportTransmitterMismatch", &monitoring.WriteReportTransmitterMismatch{ExecutionContext: ec}},
		{"WriteReportP2PConfigIncomplete", &monitoring.WriteReportP2PConfigIncomplete{ExecutionContext: ec}},
	}

	for _, tc := range errorMsgs {
		t.Run(tc.name, func(t *testing.T) {
			_, p := newTestProcessor(t)
			require.NoError(t, p.Process(ctx, tc.msg))
		})
	}
}

func TestProcessor_Process_WriteReportError_UserError_SkipsMetrics(t *testing.T) {
	_, p := newTestProcessor(t)
	msg := &monitoring.WriteReportError{
		ExecutionContext: &capmonitoring.ExecutionContext{},
		IsUserError:      true,
		Summary:          "user did something wrong",
		Cause:            "invalid input",
	}
	require.NoError(t, p.Process(t.Context(), msg))
}

func TestProcessor_Process_ViewError_UserError_SkipsMetrics(t *testing.T) {
	_, p := newTestProcessor(t)
	msg := &monitoring.ViewError{
		ExecutionContext: &capmonitoring.ExecutionContext{},
		IsUserError:      true,
		Summary:          "user did something wrong",
		Cause:            "invalid input",
	}
	require.NoError(t, p.Process(t.Context(), msg))
}

type dummyProto struct{}

func (d *dummyProto) ProtoReflect() protoreflect.Message { return nil }

func TestProcessor_Process_UnknownMessage_Noop(t *testing.T) {
	_, p := newTestProcessor(t)
	require.NoError(t, p.Process(t.Context(), &dummyProto{}))
}

func TestNewMetrics(t *testing.T) {
	metrics, err := monitoring.NewMetrics()
	require.NoError(t, err)
	assert.NotNil(t, metrics)
}
