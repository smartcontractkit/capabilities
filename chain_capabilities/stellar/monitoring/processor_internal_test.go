package monitoring

import (
	"context"
	"errors"
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"
)

type errorMetrics struct {
	err error
}

func (e errorMetrics) OnReadContractSuccess(context.Context, *ReadContractSuccess) error {
	return e.err
}

func (e errorMetrics) OnReadContractError(context.Context, *ReadContractError) error {
	return e.err
}

func (e errorMetrics) OnWriteReportSuccess(context.Context, *WriteReportSuccess) error {
	return e.err
}

func (e errorMetrics) OnWriteReportError(context.Context, *WriteReportError) error {
	return e.err
}

func (e errorMetrics) OnWriteReportTxInfoRetrievalError(context.Context, *WriteReportTxInfoRetrievalError) error {
	return e.err
}

func (e errorMetrics) OnWriteReportDuplicateTx(context.Context, *WriteReportDuplicateTx) error {
	return e.err
}

func (e errorMetrics) OnWriteReportSuccessfulEarlyReturn(context.Context, *WriteReportSuccessfulEarlyReturn) error {
	return e.err
}

func (e errorMetrics) OnWriteReportInvalidTransmissionState(context.Context, *WriteReportInvalidTransmissionState) error {
	return e.err
}

func TestProcessor_Process_MetricsErrors(t *testing.T) {
	t.Parallel()

	ec := &capmonitoring.ExecutionContext{}
	readReq := &ReadContractRequest{ContractId: "C123", Function: "get"}
	writeReq := &WriteReportRequest{ContractId: "C456", ReportSize: 1, SigsCount: 1}
	metricsErr := errors.New("metrics publish failed")

	cases := []struct {
		name string
		msg  proto.Message
	}{
		{"ReadContractSuccess", &ReadContractSuccess{Req: readReq, ExecutionContext: ec}},
		{"ReadContractError", &ReadContractError{Req: readReq, ExecutionContext: ec, IsUserError: false}},
		{"WriteReportSuccess", &WriteReportSuccess{Req: writeReq, ExecutionContext: ec}},
		{"WriteReportError", &WriteReportError{Req: writeReq, ExecutionContext: ec, IsUserError: false}},
		{"WriteReportTxInfoRetrievalError", &WriteReportTxInfoRetrievalError{Req: writeReq, ExecutionContext: ec}},
		{"WriteReportDuplicateTx", &WriteReportDuplicateTx{Req: writeReq, ExecutionContext: ec}},
		{"WriteReportSuccessfulEarlyReturn", &WriteReportSuccessfulEarlyReturn{ExecutionContext: ec}},
		{"WriteReportInvalidTransmissionState", &WriteReportInvalidTransmissionState{Req: writeReq, ExecutionContext: ec}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := &Processor{
				Lggr:    logger.Test(t),
				Metrics: errorMetrics{err: metricsErr},
			}
			err := p.Process(t.Context(), tc.msg)
			require.Error(t, err)
			require.ErrorIs(t, err, metricsErr)
		})
	}
}
