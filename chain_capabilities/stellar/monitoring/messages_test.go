package monitoring_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
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
		TsStart:             1234,
		WorkflowID:          "workflow-id",
		WorkflowOwner:       "workflow-owner",
		WorkflowExecutionID: "workflow-execution-id",
		WorkflowName:        "stellar workflow",
		WorkflowDonID:       7,
		ReferenceID:         "step-1",
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
}

func TestMessageAttributes_WriteReportNilRequest(t *testing.T) {
	t.Parallel()

	ec := newMessageBuilder().BuildExecutionContext(newTelemetryContext())

	messages := []interface {
		LogAttributes() []attribute.KeyValue
		MetricAttributes() []attribute.KeyValue
	}{
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

// TestMessageBuilder_CapabilityMetricsAttributes pins the OTel label set consumed by
// the generated v2 server wrapper via MonitoringContext.
func TestMessageBuilder_CapabilityMetricsAttributes(t *testing.T) {
	t.Parallel()

	attrs := attrsToMap(newMessageBuilder().CapabilityMetricsAttributes())
	assert.Equal(t, "stellar", attrs["chain_family_name"])
	assert.Equal(t, "testnet", attrs["chain_id"])
	assert.Equal(t, "testnet", attrs["network_name"])
	assert.Equal(t, "Stellar Testnet", attrs["network_name_full"])
	assert.Equal(t, "stellar:write-report@1.0.0", attrs["capability_id"])
	require.NotEmpty(t, attrs["capability_type"])
}
