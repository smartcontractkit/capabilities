package monitoring_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/monitoring"
)

func newMessageBuilder() *monitoring.MessageBuilder {
	return monitoring.NewMessageBuilder(
		types.ChainInfo{
			FamilyName:      "stellar",
			ChainID:         "testnet",
			NetworkName:     "testnet",
			NetworkNameFull: "Stellar Testnet",
		},
		capabilities.CapabilityInfo{
			ID:             "stellar:write-report@1.0.0",
			CapabilityType: capabilities.CapabilityTypeAction,
		},
		"node-1",
	)
}

func newTelemetryContext() monitoring.TelemetryContext {
	return monitoring.TelemetryContext{
		TsStart: 1234,
		RequestMetadata: capabilities.RequestMetadata{
			WorkflowID:          "workflow-id",
			WorkflowOwner:       "workflow-owner",
			WorkflowExecutionID: "workflow-execution-id",
			WorkflowName:        "stellar workflow",
			WorkflowDonID:       7,
			ReferenceID:         "step-1",
		},
	}
}

func attrsToMap(attrs []attribute.KeyValue) map[string]any {
	out := make(map[string]any, len(attrs))
	for _, attr := range attrs {
		if attr.Valid() {
			out[string(attr.Key)] = attr.Value.AsInterface()
		}
	}
	return out
}

func TestMessageBuilder_ReadContractMessages(t *testing.T) {
	builder := newMessageBuilder()
	tc := newTelemetryContext()
	req := stellartypes.SimulateTransactionRequest{
		ContractID:    "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
		Function:      "get",
		SourceAccount: "GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN7",
	}

	initiated := builder.BuildReadContractInitiated(tc, req)
	require.NotNil(t, initiated.Req)
	assert.Equal(t, req.ContractID, initiated.Req.GetContractId())
	assert.EqualValues(t, 7, attrsToMap(initiated.MetricAttributes())["workflow_don_id"])

	success := builder.BuildReadContractSuccess(tc, req, 12, 100)
	successAttrs := attrsToMap(success.LogAttributes())
	assert.EqualValues(t, int64(12), successAttrs["result_len"])
	assert.EqualValues(t, int64(100), successAttrs["ledger_sequence"])

	readErr := builder.BuildReadContractError(tc, req, "summary", caperrors.NewPublicUserError(assert.AnError, caperrors.InvalidArgument))
	errAttrs := attrsToMap(readErr.LogAttributes())
	assert.Equal(t, "summary", errAttrs["summary"])
	assert.Equal(t, true, errAttrs["is_user_error"])
	assert.Equal(t, req.ContractID, errAttrs["contract_id"])
	assert.EqualValues(t, 7, attrsToMap(readErr.MetricAttributes())["workflow_don_id"])

	readSystemErr := builder.BuildReadContractError(tc, req, "system failure", caperrors.NewPublicSystemError(assert.AnError, caperrors.Unknown))
	systemErrAttrs := attrsToMap(readSystemErr.LogAttributes())
	assert.Equal(t, false, systemErrAttrs["is_user_error"])

	nilRead := &monitoring.ReadContractInitiated{
		ExecutionContext: newMessageBuilder().BuildExecutionContext(newTelemetryContext()),
	}
	assert.Equal(t, "nil request", attrsToMap(nilRead.LogAttributes())["contract_id"])

	reqNoSource := stellartypes.SimulateTransactionRequest{
		ContractID: req.ContractID,
		Function:   req.Function,
	}
	noSourceAttrs := attrsToMap(builder.BuildReadContractInitiated(tc, reqNoSource).LogAttributes())
	_, hasSource := noSourceAttrs["source_account"]
	assert.False(t, hasSource)
}

func TestMessageBuilder_WriteReportMessages(t *testing.T) {
	builder := newMessageBuilder()
	tc := newTelemetryContext()
	req := &stellarcap.WriteReportRequest{
		ContractId: "CA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUWDA",
		Report: &workflowpb.ReportResponse{
			RawReport: []byte{1, 2, 3, 4},
			Sigs:      []*workflowpb.AttributedSignature{{}, {}},
		},
	}

	initiated := builder.BuildWriteReportInitiated(tc, req)
	require.NotNil(t, initiated.Req)
	assert.Equal(t, req.ContractId, initiated.Req.GetContractId())
	assert.Equal(t, uint64(4), initiated.Req.GetReportSize())
	assert.Equal(t, uint32(2), initiated.Req.GetSigsCount())

	initiatedAttrs := attrsToMap(initiated.LogAttributes())
	assert.Equal(t, req.ContractId, initiatedAttrs["contract_id"])
	assert.EqualValues(t, int64(4), initiatedAttrs["report_size"])
	assert.EqualValues(t, int64(2), initiatedAttrs["sigs_count"])
	assert.EqualValues(t, 7, attrsToMap(initiated.MetricAttributes())["workflow_don_id"])

	success := builder.BuildWriteReportSuccess(tc, req)
	assert.Equal(t, req.ContractId, attrsToMap(success.LogAttributes())["contract_id"])

	writeErr := builder.BuildWriteReportError(tc, req, "summary", caperrors.NewPublicUserError(assert.AnError, caperrors.InvalidArgument))
	errAttrs := attrsToMap(writeErr.LogAttributes())
	assert.Equal(t, "summary", errAttrs["summary"])
	assert.Equal(t, true, errAttrs["is_user_error"])

	writeSystemErr := builder.BuildWriteReportError(tc, req, "execute failed", caperrors.NewPublicSystemError(assert.AnError, caperrors.Unknown))
	assert.Equal(t, false, attrsToMap(writeSystemErr.LogAttributes())["is_user_error"])
	assert.EqualValues(t, 7, attrsToMap(writeSystemErr.MetricAttributes())["workflow_don_id"])

	txInfoErr := builder.BuildWriteReportTxInfoRetrievalError(tc, req, "abc123", "rpc down")
	txInfoAttrs := attrsToMap(txInfoErr.LogAttributes())
	assert.Equal(t, "abc123", txInfoAttrs["tx_hash"])
	assert.Equal(t, "Failed to retrieve transaction info for tx: abc123", txInfoAttrs["summary"])

	txInfoNoHash := builder.BuildWriteReportTxInfoRetrievalError(tc, req, "", "rpc down")
	assert.Equal(t, "Failed to retrieve transaction info", attrsToMap(txInfoNoHash.LogAttributes())["summary"])

	duplicateTx := builder.BuildWriteReportDuplicateTx(tc, req, "local-hash", "canonical-hash")
	dupAttrs := attrsToMap(duplicateTx.LogAttributes())
	assert.Equal(t, "local-hash", dupAttrs["duplicate_tx_hash"])
	assert.Equal(t, "canonical-hash", dupAttrs["canonical_tx_hash"])
	assert.Equal(t, req.ContractId, dupAttrs["contract_id"])

	dupOnlyLocal := builder.BuildWriteReportDuplicateTx(tc, req, "local-hash", "")
	dupOnlyLocalAttrs := attrsToMap(dupOnlyLocal.LogAttributes())
	assert.Equal(t, "local-hash", dupOnlyLocalAttrs["duplicate_tx_hash"])
	_, hasCanonical := dupOnlyLocalAttrs["canonical_tx_hash"]
	assert.False(t, hasCanonical)

	earlyReturn := builder.BuildWriteReportSuccessfulEarlyReturn(tc)
	earlyReturnAttrs := attrsToMap(earlyReturn.LogAttributes())
	assert.Equal(t, "node-1", earlyReturnAttrs["source_id"])
	assert.NotNil(t, earlyReturn.ExecutionContext)

	invalidState := builder.BuildWriteReportInvalidTransmissionState(
		tc, req, 3, true, false, "receiver:report:exec", "transmitter", "summary", "cause",
	)
	invalidAttrs := attrsToMap(invalidState.LogAttributes())
	assert.EqualValues(t, int64(3), invalidAttrs["transmission_state"])
	assert.Equal(t, true, invalidAttrs["invalid_receiver"])
	assert.Equal(t, false, invalidAttrs["success"])
	assert.Equal(t, "transmitter", invalidAttrs["transmitter"])
	assert.EqualValues(t, 7, attrsToMap(invalidState.MetricAttributes())["workflow_don_id"])

	nilInitiated := builder.BuildWriteReportInitiated(tc, nil)
	nilAttrs := attrsToMap(nilInitiated.LogAttributes())
	assert.Equal(t, "nil request", nilAttrs["contract_id"])
}

func TestMessageAttributes_WriteReportNilRequest(t *testing.T) {
	t.Parallel()

	ec := newMessageBuilder().BuildExecutionContext(newTelemetryContext())

	messages := []interface {
		LogAttributes() []attribute.KeyValue
		MetricAttributes() []attribute.KeyValue
	}{
		&monitoring.WriteReportInitiated{ExecutionContext: ec},
		&monitoring.WriteReportSuccess{ExecutionContext: ec},
		&monitoring.WriteReportError{ExecutionContext: ec, Summary: "summary"},
		&monitoring.WriteReportTxInfoRetrievalError{ExecutionContext: ec, Summary: "summary"},
		&monitoring.WriteReportDuplicateTx{ExecutionContext: ec, DuplicateTxHash: "dup"},
		&monitoring.WriteReportInvalidTransmissionState{ExecutionContext: ec, Summary: "summary"},
	}

	for _, msg := range messages {
		attrs := attrsToMap(msg.LogAttributes())
		assert.Equal(t, "nil request", attrs["contract_id"])
		assert.NotEmpty(t, msg.MetricAttributes())
	}
}

func TestMessageAttributes_ReadContractNilRequest(t *testing.T) {
	t.Parallel()

	ec := newMessageBuilder().BuildExecutionContext(newTelemetryContext())
	msg := &monitoring.ReadContractSuccess{ExecutionContext: ec}
	attrs := attrsToMap(msg.LogAttributes())
	assert.Equal(t, "nil request", attrs["contract_id"])
	assert.NotEmpty(t, msg.MetricAttributes())
}

func TestMessageAttributes_WriteReportWithRequest(t *testing.T) {
	t.Parallel()

	builder := newMessageBuilder()
	tc := newTelemetryContext()
	req := &monitoring.WriteReportRequest{
		ContractId: "CA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJUWDA",
		ReportSize: 2,
		SigsCount:  1,
	}
	ec := builder.BuildExecutionContext(tc)

	success := &monitoring.WriteReportSuccess{Req: req, ExecutionContext: ec}
	successAttrs := attrsToMap(success.LogAttributes())
	assert.Equal(t, req.ContractId, successAttrs["contract_id"])
	assert.NotEmpty(t, success.MetricAttributes())

	txInfoErr := &monitoring.WriteReportTxInfoRetrievalError{
		Req: req, ExecutionContext: ec, Summary: "summary", TxHash: "hash",
	}
	txInfoAttrs := attrsToMap(txInfoErr.LogAttributes())
	assert.Equal(t, "hash", txInfoAttrs["tx_hash"])
	assert.NotEmpty(t, txInfoErr.MetricAttributes())

	duplicateTx := &monitoring.WriteReportDuplicateTx{
		Req: req, ExecutionContext: ec, DuplicateTxHash: "dup", CanonicalTxHash: "canonical",
	}
	dupAttrs := attrsToMap(duplicateTx.LogAttributes())
	assert.Equal(t, "dup", dupAttrs["duplicate_tx_hash"])
	assert.Equal(t, "canonical", dupAttrs["canonical_tx_hash"])
	assert.NotEmpty(t, duplicateTx.MetricAttributes())

	earlyReturn := &monitoring.WriteReportSuccessfulEarlyReturn{ExecutionContext: ec}
	assert.NotEmpty(t, earlyReturn.LogAttributes())
	assert.NotEmpty(t, earlyReturn.MetricAttributes())
}
