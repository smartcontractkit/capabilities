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
		basic: commoncapbeholder.NewMetricsInfoCapBasic(ns("call_contract_error"), commonbeholder.ToSchemaFullName(&CallContractSuccess{})),
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

func (m *Metrics) OnCallContractSuccess(ctx context.Context, msg *CallContractSuccess) error {
	// Emit basic metrics (count, timestamps)
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.CallContractSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnCallContractError(ctx context.Context, msg *CallContractError) error {
	// Emit basic metrics (count, timestamps)
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.CallContractError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

// Attributes returns the attributes for the CallContractSuccess message to be used in metrics
func (r *CallContractSuccess) Attributes() []attribute.KeyValue {
	ctx := commoncapbeholder.ExecutionMetadata{
		SourceID:                 r.ExecutionContext.MetaSourceId,
		ChainFamilyName:          r.ExecutionContext.MetaChainFamilyName,
		ChainID:                  r.ExecutionContext.MetaChainId,
		NetworkName:              r.ExecutionContext.MetaNetworkName,
		NetworkNameFull:          r.ExecutionContext.MetaNetworkNameFull,
		WorkflowID:               r.ExecutionContext.MetaWorkflowId,
		WorkflowOwner:            r.ExecutionContext.MetaWorkflowOwner,
		WorkflowExecutionID:      r.ExecutionContext.MetaWorkflowExecutionId,
		WorkflowName:             r.ExecutionContext.MetaWorkflowName,
		WorkflowDonID:            r.ExecutionContext.MetaWorkflowDonId,
		WorkflowDonConfigVersion: r.ExecutionContext.MetaWorkflowDonConfigVersion,
		ReferenceID:              r.ExecutionContext.MetaReferenceId,
		CapabilityType:           r.ExecutionContext.MetaCapabilityType,
		CapabilityID:             r.ExecutionContext.MetaCapabilityId,
	}
	// Base attributes
	attrs := []attribute.KeyValue{
		attribute.Int64("block_number", r.BlockNumber),
		attribute.String("contract_address", r.ContractAddress),
	}
	return append(attrs, ctx.Attributes()...)
}

// Attributes returns the attributes for the CallContractError message to be used in metrics
func (r *CallContractError) Attributes() []attribute.KeyValue {
	ctx := commoncapbeholder.ExecutionMetadata{
		SourceID:                 r.ExecutionContext.MetaSourceId,
		ChainFamilyName:          r.ExecutionContext.MetaChainFamilyName,
		ChainID:                  r.ExecutionContext.MetaChainId,
		NetworkName:              r.ExecutionContext.MetaNetworkName,
		NetworkNameFull:          r.ExecutionContext.MetaNetworkNameFull,
		WorkflowID:               r.ExecutionContext.MetaWorkflowId,
		WorkflowOwner:            r.ExecutionContext.MetaWorkflowOwner,
		WorkflowExecutionID:      r.ExecutionContext.MetaWorkflowExecutionId,
		WorkflowName:             r.ExecutionContext.MetaWorkflowName,
		WorkflowDonID:            r.ExecutionContext.MetaWorkflowDonId,
		WorkflowDonConfigVersion: r.ExecutionContext.MetaWorkflowDonConfigVersion,
		ReferenceID:              r.ExecutionContext.MetaReferenceId,
		CapabilityType:           r.ExecutionContext.MetaCapabilityType,
		CapabilityID:             r.ExecutionContext.MetaCapabilityId,
	}
	// Base attributes
	attrs := []attribute.KeyValue{
		attribute.Int64("block_number", r.BlockNumber),
		attribute.String("contract_address", r.ContractAddress),
		attribute.String("summary", r.Summary),
	}
	return append(attrs, ctx.Attributes()...)
}
