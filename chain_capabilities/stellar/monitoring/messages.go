package monitoring

import (
	"strconv"

	"go.opentelemetry.io/otel/attribute"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
)

// MessageBuilder constructs telemetry messages for Stellar calls.
// Embeds the common MessageBuilder for shared BuildExecutionContext and RequestLggr.
type MessageBuilder struct {
	*commonmon.MessageBuilder
}

// NewMessageBuilder creates a new Stellar-specific MessageBuilder.
func NewMessageBuilder(chainInfo types.ChainInfo, capInfo capabilities.CapabilityInfo, nodeAddress string) *MessageBuilder {
	return &MessageBuilder{
		MessageBuilder: commonmon.NewMessageBuilder(chainInfo, capInfo, nodeAddress),
	}
}

func (m *MessageBuilder) BuildReadContractInitiated(tc commonmon.TelemetryContext, req stellartypes.SimulateTransactionRequest) *ReadContractInitiated {
	return &ReadContractInitiated{
		Req:              convertReadContractRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildReadContractSuccess(tc commonmon.TelemetryContext, req stellartypes.SimulateTransactionRequest, resultLen uint64, ledgerSequence uint32) *ReadContractSuccess {
	return &ReadContractSuccess{
		Req:              convertReadContractRequest(req),
		ResultLen:        resultLen,
		LedgerSequence:   ledgerSequence,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildReadContractError(tc commonmon.TelemetryContext, req stellartypes.SimulateTransactionRequest, summary string, err caperrors.Error) commonmon.ErrorMessage {
	return &ReadContractError{
		Req:              convertReadContractRequest(req),
		Summary:          summary,
		Cause:            err.Error(),
		IsUserError:      err.Origin() == caperrors.OriginUser,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

// convertReadContractRequest extracts the non-sensitive subset of the request for telemetry
// (raw argument values are intentionally omitted; only the count is recorded).
func convertReadContractRequest(req stellartypes.SimulateTransactionRequest) *ReadContractRequest {
	return &ReadContractRequest{
		ContractId:    req.ContractID,
		Function:      req.Function,
		ArgsCount:     uint64(len(req.Args)),
		SourceAccount: req.SourceAccount,
	}
}

func (r *ReadContractInitiated) LogAttributes() []attribute.KeyValue {
	return append(readContractRequestLogAttributes(r.Req), r.ExecutionContext.LogAttributes()...)
}

func (r *ReadContractInitiated) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *ReadContractSuccess) LogAttributes() []attribute.KeyValue {
	return append(append(readContractRequestLogAttributes(r.Req),
		attribute.String("result_len", strconv.FormatUint(r.GetResultLen(), 10)),
		attribute.String("ledger_sequence", strconv.FormatUint(uint64(r.GetLedgerSequence()), 10)),
	), r.ExecutionContext.LogAttributes()...)
}

func (r *ReadContractSuccess) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *ReadContractError) LogAttributes() []attribute.KeyValue {
	return append(append(readContractRequestLogAttributes(r.Req),
		attribute.String("summary", r.GetSummary()),
		attribute.Bool("isUserError", r.GetIsUserError()),
	), r.ExecutionContext.LogAttributes()...)
}

func (r *ReadContractError) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func readContractRequestLogAttributes(req *ReadContractRequest) []attribute.KeyValue {
	if req == nil {
		return []attribute.KeyValue{
			attribute.String("contract_id", "nil request"),
			attribute.String("function", ""),
		}
	}
	attrs := []attribute.KeyValue{
		attribute.String("contract_id", req.GetContractId()),
		attribute.String("function", req.GetFunction()),
		attribute.String("args_count", strconv.FormatUint(req.GetArgsCount(), 10)),
	}
	if req.GetSourceAccount() != "" {
		attrs = append(attrs, attribute.String("source_account", req.GetSourceAccount()))
	}
	return attrs
}
