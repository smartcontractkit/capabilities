package monitoring_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"

	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/monitoring"
)

func testExecutionContext(t *testing.T) *capmonitoring.ExecutionContext {
	t.Helper()
	ec := newMessageBuilder().BuildExecutionContext(newTelemetryContext())
	ec.MetaCapabilityTimestampStart = 100
	ec.MetaCapabilityTimestampEmit = 250
	return ec
}

func TestMetrics_OnBasicCapHandlers(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	metrics, err := monitoring.NewMetrics()
	require.NoError(t, err)

	ec := testExecutionContext(t)
	readReq := &monitoring.ReadContractRequest{
		ContractId:    "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
		Function:      "get",
		ArgsCount:     2,
		SourceAccount: "GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN7",
	}
	writeReq := &monitoring.WriteReportRequest{
		ContractId: "CA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUWDA",
		ReportSize: 4,
		SigsCount:  2,
	}

	require.NoError(t, metrics.OnReadContractSuccess(ctx, &monitoring.ReadContractSuccess{
		Req: readReq, ResultLen: 12, LedgerSequence: 100, ExecutionContext: ec,
	}))
	require.NoError(t, metrics.OnReadContractError(ctx, &monitoring.ReadContractError{
		Req: readReq, Summary: "summary", IsUserError: false, ExecutionContext: ec,
	}))
	require.NoError(t, metrics.OnWriteReportSuccess(ctx, &monitoring.WriteReportSuccess{
		Req: writeReq, ExecutionContext: ec,
	}))
	require.NoError(t, metrics.OnWriteReportError(ctx, &monitoring.WriteReportError{
		Req: writeReq, Summary: "summary", IsUserError: false, ExecutionContext: ec,
	}))
	require.NoError(t, metrics.OnWriteReportDuplicateTx(ctx, &monitoring.WriteReportDuplicateTx{
		Req: writeReq, DuplicateTxHash: "dup", CanonicalTxHash: "canonical", ExecutionContext: ec,
	}))
	require.NoError(t, metrics.OnWriteReportTxInfoRetrievalError(ctx, &monitoring.WriteReportTxInfoRetrievalError{
		Req: writeReq, Summary: "summary", TxHash: "hash", ExecutionContext: ec,
	}))
	require.NoError(t, metrics.OnWriteReportSuccessfulEarlyReturn(ctx, &monitoring.WriteReportSuccessfulEarlyReturn{
		ExecutionContext: ec,
	}))
	require.NoError(t, metrics.OnWriteReportInvalidTransmissionState(ctx, &monitoring.WriteReportInvalidTransmissionState{
		Req: writeReq, Summary: "summary", TransmissionState: 3, InvalidReceiver: true,
		TransmissionId: "receiver:report:exec", Transmitter: "transmitter", ExecutionContext: ec,
	}))
}
