package monitoring

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"

	commoncapbeholder "github.com/smartcontractkit/capabilities/monitoring"

	commonbeholder "github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

// ns returns a namespaced metric name
func ns(name string) string {
	return fmt.Sprintf("evm_capability_%s", name)
}

// Metrics holds all per-method instruments
type Metrics struct {
	CallContractSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}

	CallContractError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
}

// NewMetrics constructs all counters & histograms bound to a given chainID
func NewMetrics() (*Metrics, error) {
	m := &Metrics{}
	var err error

	// Metric definitions for successful calls
	callContractSuccess := struct {
		basic commoncapbeholder.MetricsInfoCapBasic
	}{
		basic: commoncapbeholder.NewMetricsInfoCapBasic(ns("call_contract_success"), commonbeholder.ToSchemaFullName(&CallContractSuccess{})),
	}

	callContractError := struct {
		basic commoncapbeholder.MetricsInfoCapBasic
	}{
		basic: commoncapbeholder.NewMetricsInfoCapBasic(ns("call_contract_error"), commonbeholder.ToSchemaFullName(&CallContractError{})),
	}

	m.CallContractSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(callContractSuccess.basic)
	if err != nil {
		return nil, fmt.Errorf("failed to create call contract success capability metric: %w", err)
	}

	m.CallContractError.basic, err = commoncapbeholder.NewMetricsCapBasic(callContractError.basic)
	if err != nil {
		return nil, fmt.Errorf("failed to create call contract error capability metric: %w", err)
	}

	return m, nil
}

func (m *Metrics) OnCallContractInitiated(ctx context.Context, msg *CallContractInitiated) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.CallContractSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnCallContractSuccess(ctx context.Context, msg *CallContractSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.CallContractSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnCallContractError(ctx context.Context, msg *CallContractError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.CallContractError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

// Attributes returns the attributes for the CallContractSuccess message to be used in metrics
func (r *CallContractInitiated) Attributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.Int64("block_number", r.Req.GetBlockNumber()),
		attribute.String("contract_address", r.Req.GetContractAddress()),
	}
	return append(attrs, executionMetadata(r.ExecutionContext).Attributes()...)
}

// Attributes returns the attributes for the CallContractSuccess message to be used in metrics
func (r *CallContractSuccess) Attributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.Int64("block_number", r.Req.GetBlockNumber()),
		attribute.String("contract_address", r.Req.GetContractAddress()),
	}
	return append(attrs, executionMetadata(r.ExecutionContext).Attributes()...)
}

// Attributes returns the attributes for the CallContractError message to be used in metrics
func (r *CallContractError) Attributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.Int64("block_number", r.Req.GetBlockNumber()),
		attribute.String("contract_address", r.Req.GetContractAddress()),
		attribute.String("summary", r.GetSummary()),
	}
	return append(attrs, executionMetadata(r.ExecutionContext).Attributes()...)
}

func executionMetadata(ec *commoncapbeholder.ExecutionContext) commoncapbeholder.ExecutionMetadata {
	ctx := commoncapbeholder.ExecutionMetadata{
		SourceID:                 ec.MetaSourceId,
		ChainFamilyName:          ec.MetaChainFamilyName,
		ChainID:                  ec.MetaChainId,
		NetworkName:              ec.MetaNetworkName,
		NetworkNameFull:          ec.MetaNetworkNameFull,
		WorkflowID:               ec.MetaWorkflowId,
		WorkflowOwner:            ec.MetaWorkflowOwner,
		WorkflowExecutionID:      ec.MetaWorkflowExecutionId,
		WorkflowName:             ec.MetaWorkflowName,
		WorkflowDonID:            ec.MetaWorkflowDonId,
		WorkflowDonConfigVersion: ec.MetaWorkflowDonConfigVersion,
		ReferenceID:              ec.MetaReferenceId,
		CapabilityType:           ec.MetaCapabilityType,
		CapabilityID:             ec.MetaCapabilityId,
	}
	return ctx
}
