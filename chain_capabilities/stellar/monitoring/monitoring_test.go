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

	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/monitoring"
)

func newTestProcessor(t *testing.T) (monitoring.Metrics, beholder.ProtoProcessor) {
	t.Helper()

	lggr := logger.Test(t)
	metrics, err := monitoring.NewMetrics()
	require.NoError(t, err)
	return metrics, &monitoring.Processor{Lggr: lggr, Metrics: &metrics}
}

func TestProcessor_Process_InitiatedMessage(t *testing.T) {
	ctx := t.Context()
	ec := &capmonitoring.ExecutionContext{}

	initiatedMsgs := []struct {
		name string
		msg  proto.Message
	}{
		{"ReadContractInitiated", &monitoring.ReadContractInitiated{ExecutionContext: ec}},
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
	ec := &capmonitoring.ExecutionContext{
		MetaCapabilityTimestampStart: 10,
		MetaCapabilityTimestampEmit:  20,
	}
	readReq := &monitoring.ReadContractRequest{ContractId: "C123", Function: "get"}
	writeReq := &monitoring.WriteReportRequest{ContractId: "C456", ReportSize: 1, SigsCount: 1}

	successMsgs := []struct {
		name string
		msg  proto.Message
	}{
		{"ReadContractSuccess", &monitoring.ReadContractSuccess{Req: readReq, ExecutionContext: ec, ResultLen: 1, LedgerSequence: 2}},
		{"WriteReportSuccess", &monitoring.WriteReportSuccess{Req: writeReq, ExecutionContext: ec}},
		{"WriteReportDuplicateTx", &monitoring.WriteReportDuplicateTx{Req: writeReq, ExecutionContext: ec, DuplicateTxHash: "a", CanonicalTxHash: "b"}},
		{"WriteReportSuccessfulEarlyReturn", &monitoring.WriteReportSuccessfulEarlyReturn{ExecutionContext: ec}},
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
	ec := &capmonitoring.ExecutionContext{
		MetaCapabilityTimestampStart: 10,
		MetaCapabilityTimestampEmit:  20,
	}
	readReq := &monitoring.ReadContractRequest{ContractId: "C123", Function: "get"}
	writeReq := &monitoring.WriteReportRequest{ContractId: "C456", ReportSize: 1, SigsCount: 1}

	errorMsgs := []struct {
		name string
		msg  proto.Message
	}{
		{"ReadContractError", &monitoring.ReadContractError{Req: readReq, ExecutionContext: ec, IsUserError: false, Summary: "fail"}},
		{"WriteReportError", &monitoring.WriteReportError{Req: writeReq, ExecutionContext: ec, IsUserError: false, Summary: "fail"}},
		{"WriteReportTxInfoRetrievalError", &monitoring.WriteReportTxInfoRetrievalError{Req: writeReq, ExecutionContext: ec, Summary: "fail", TxHash: "hash"}},
		{"WriteReportInvalidTransmissionState", &monitoring.WriteReportInvalidTransmissionState{Req: writeReq, ExecutionContext: ec, Summary: "fail"}},
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

func TestProcessor_Process_ReadContractError_UserError_SkipsMetrics(t *testing.T) {
	_, p := newTestProcessor(t)
	msg := &monitoring.ReadContractError{
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
