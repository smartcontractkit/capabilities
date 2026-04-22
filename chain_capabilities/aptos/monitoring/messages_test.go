package monitoring_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	sdkpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/monitoring"
)

func newMessageBuilder() *monitoring.MessageBuilder {
	return monitoring.NewMessageBuilder(
		types.ChainInfo{
			FamilyName:      "aptos",
			ChainID:         "1",
			NetworkName:     "testnet",
			NetworkNameFull: "Aptos Testnet",
		},
		capabilities.CapabilityInfo{
			ID:             "aptos:view@1.0.0",
			CapabilityType: capabilities.CapabilityTypeAction,
		},
		"node-1",
	)
}

func newTelemetryContext() monitoring.TelemetryContext {
	return monitoring.TelemetryContext{
		TsStart: 1234,
		RequestMetadata: capabilities.RequestMetadata{
			WorkflowID:               "workflow-id",
			WorkflowOwner:            "workflow-owner",
			WorkflowExecutionID:      "workflow-execution-id",
			WorkflowName:             hex.EncodeToString([]byte("aptos workflow")),
			WorkflowDonID:            7,
			WorkflowDonConfigVersion: 2,
			ReferenceID:              "step-1",
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

func TestMessageBuilder_ViewMessages(t *testing.T) {
	builder := newMessageBuilder()
	tc := newTelemetryContext()
	requestedLedgerVersion := uint64(77)
	req := &aptoscap.ViewRequest{
		Payload: &aptoscap.ViewPayload{
			Module:   &aptoscap.ModuleID{Address: []byte{0x01, 0x02}, Name: "coin"},
			Function: "balance",
		},
		LedgerVersion: &requestedLedgerVersion,
	}

	initiated := builder.BuildViewInitiated(tc, req)
	require.NotNil(t, initiated)
	require.NotNil(t, initiated.Req)
	require.Equal(t, []byte{0x01, 0x02}, initiated.Req.ModuleAddress)
	require.Equal(t, "coin", initiated.Req.ModuleName)
	require.Equal(t, "balance", initiated.Req.Function)
	require.NotNil(t, initiated.Req.RequestedLedgerVersion)
	require.Equal(t, requestedLedgerVersion, initiated.Req.GetRequestedLedgerVersion())

	initiatedAttrs := attrsToMap(initiated.LogAttributes())
	assert.Equal(t, "0102", initiatedAttrs["module_address"])
	assert.Equal(t, "coin", initiatedAttrs["module_name"])
	assert.Equal(t, "balance", initiatedAttrs["function"])
	assert.Equal(t, "77", initiatedAttrs["requested_ledger_version"])
	assert.EqualValues(t, 7, initiatedAttrs["workflow_don_id"])
	assert.Equal(t, "node-1", initiatedAttrs["source_id"])
	assert.Equal(t, "aptos workflow", initiatedAttrs["workflow_name"])

	success := builder.BuildViewSuccess(tc, req, 12)
	successAttrs := attrsToMap(success.LogAttributes())
	assert.Equal(t, "12", successAttrs["response_len"])
	assert.Equal(t, "0102", successAttrs["module_address"])
	assert.EqualValues(t, 7, attrsToMap(success.MetricAttributes())["workflow_don_id"])

	zeroLenSuccess := builder.BuildViewSuccess(tc, req, 0)
	assert.Zero(t, zeroLenSuccess.GetResponseLen())

	viewErr := builder.BuildViewError(tc, req, "summary", "cause", true)
	errAttrs := attrsToMap(viewErr.LogAttributes())
	assert.Equal(t, "summary", errAttrs["summary"])
	assert.Equal(t, true, errAttrs["isUserError"])
	assert.Equal(t, "0102", errAttrs["module_address"])

	nilInitiated := builder.BuildViewInitiated(tc, nil)
	nilAttrs := attrsToMap(nilInitiated.LogAttributes())
	assert.Equal(t, "nil module", nilAttrs["module_address"])
	assert.Equal(t, "", nilAttrs["module_name"])
	assert.Equal(t, "", nilAttrs["function"])
}

func TestMessageBuilder_WriteReportMessages(t *testing.T) {
	builder := newMessageBuilder()
	tc := newTelemetryContext()
	req := &aptoscap.WriteReportRequest{
		Receiver: []byte{0xaa, 0xbb},
		GasConfig: &aptoscap.GasConfig{
			MaxGasAmount: 11,
			GasUnitPrice: 22,
		},
		Report: &sdkpb.ReportResponse{
			ConfigDigest:  []byte{0x01},
			SeqNr:         9,
			ReportContext: []byte{0x02},
			RawReport:     []byte{0x03},
			Sigs: []*sdkpb.AttributedSignature{
				{Signature: []byte{0x04}, SignerId: 5},
			},
		},
	}

	initiated := builder.BuildWriteReportInitiated(tc, req)
	require.NotNil(t, initiated)
	require.NotNil(t, initiated.Req)
	require.NotNil(t, initiated.Req.GasConfig)
	require.EqualValues(t, 11, initiated.Req.GasConfig.MaxGasAmount)
	require.EqualValues(t, 22, initiated.Req.GasConfig.GasUnitPrice)
	require.NotNil(t, initiated.Req.Report)
	require.Len(t, initiated.Req.Report.Sigs, 1)
	assert.EqualValues(t, 5, initiated.Req.Report.Sigs[0].SignerId)
	assert.Equal(t, "aabb", attrsToMap(initiated.LogAttributes())["receiver"])

	nilInitiated := builder.BuildWriteReportInitiated(tc, nil)
	assert.Equal(t, "nil receiver", attrsToMap(nilInitiated.LogAttributes())["receiver"])

	success := builder.BuildWriteReportSuccess(tc, req)
	assert.Equal(t, "aabb", attrsToMap(success.LogAttributes())["receiver"])

	writeErr := builder.BuildWriteReportError(tc, req, "summary", "cause", false)
	writeErrAttrs := attrsToMap(writeErr.LogAttributes())
	assert.Equal(t, "summary", writeErrAttrs["summary"])
	assert.Equal(t, false, writeErrAttrs["isUserError"])

	feeErrNoHash := builder.BuildWriteReportTxFeeCalculationError(tc, req, "", "fee cause")
	feeErrNoHashAttrs := attrsToMap(feeErrNoHash.LogAttributes())
	assert.Equal(t, "Failed to calculate transaction fee", feeErrNoHashAttrs["summary"])
	_, hasTxHash := feeErrNoHashAttrs["tx_hash"]
	assert.False(t, hasTxHash)

	feeErrWithHash := builder.BuildWriteReportTxFeeCalculationError(tc, req, "0xabc", "fee cause")
	feeErrWithHashAttrs := attrsToMap(feeErrWithHash.LogAttributes())
	assert.Equal(t, "Failed to calculate transaction fee for tx: 0xabc", feeErrWithHashAttrs["summary"])
	assert.Equal(t, "0xabc", feeErrWithHashAttrs["tx_hash"])

	duplicateTx := builder.BuildWriteReportDuplicateTx(tc, req, "0xdup", "0xok")
	duplicateAttrs := attrsToMap(duplicateTx.LogAttributes())
	assert.Equal(t, "0xdup", duplicateAttrs["duplicate_tx_hash"])
	assert.Equal(t, "0xok", duplicateAttrs["successful_tx_hash"])

	earlyReturn := builder.BuildWriteReportSuccessfulEarlyReturn(tc)
	assert.Equal(t, "node-1", attrsToMap(earlyReturn.LogAttributes())["source_id"])

	transmitterMismatch := builder.BuildWriteReportTransmitterMismatch(tc, "transmitter-a", []string{"one", "two"})
	transmitterAttrs := attrsToMap(transmitterMismatch.LogAttributes())
	assert.Equal(t, "transmitter-a", transmitterAttrs["transmitter"])
	assert.Equal(t, []string{"one", "two"}, transmitterAttrs["orderedTransmitters"])

	p2pIncomplete := builder.BuildWriteReportP2pConfigIncomplete(tc, 3)
	assert.EqualValues(t, 3, attrsToMap(p2pIncomplete.LogAttributes())["position"])
}
