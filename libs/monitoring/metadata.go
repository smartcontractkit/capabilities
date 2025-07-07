package monitoring

import (
	"encoding/hex"

	"go.opentelemetry.io/otel/attribute"
)

const (
	// WorkflowExecutionIDShortLen is the length of the short version of the WorkflowExecutionID (label)
	WorkflowExecutionIDShortLen = 3 // first 3 characters, 16^3 = 4.096 possibilities (mid-high cardinality)
)

type ChainInfo struct {
	FamilyName      string
	ChainID         string
	NetworkName     string
	NetworkNameFull string
}

// Attributes returns common attributes used for metrics
func (x *ExecutionContext) Attributes() []attribute.KeyValue {
	// Decode workflow name attribute for output
	workflowName := x.decodeWorkflowName()

	return []attribute.KeyValue{
		// Execution Context - Source
		attribute.String("source_id", ValOrUnknown(x.GetMetaSourceId())),
		// Execution Context - Chain
		attribute.String("chain_family_name", ValOrUnknown(x.GetMetaChainFamilyName())),
		attribute.String("chain_id", ValOrUnknown(x.GetMetaChainId())),
		attribute.String("network_name", ValOrUnknown(x.GetMetaNetworkName())),
		attribute.String("network_name_full", ValOrUnknown(x.GetMetaNetworkNameFull())),
		// Execution Context - Workflow (capabilities.RequestMetadata)
		attribute.String("workflow_id", ValOrUnknown(x.GetMetaWorkflowId())),
		attribute.String("workflow_owner", ValOrUnknown(x.GetMetaWorkflowOwner())),
		// Notice: We lower the cardinality on the WorkflowExecutionID so it can be used by metrics
		// This label has good chances to be unique per workflow, in a reasonable bounded time window
		// TODO: enable this when sufficiently tested (PromQL queries like alerts might need to change if this is used)
		//attribute.String("workflow_execution_id_short", ValShortOrUnknown(x.GetMetaWorkflowExecutionId(), WorkflowExecutionIDShortLen)),
		attribute.String("workflow_name", ValOrUnknown(workflowName)),
		attribute.Int64("workflow_don_id", int64(x.GetMetaWorkflowDonId())),
		attribute.Int64("workflow_don_config_version", int64(x.GetMetaWorkflowDonConfigVersion())),
		attribute.String("reference_id", ValOrUnknown(x.GetMetaReferenceId())),
		// Execution Context - Capability
		attribute.String("capability_type", ValOrUnknown(x.GetMetaCapabilityType())),
		attribute.String("capability_id", ValOrUnknown(x.GetMetaCapabilityId())),
	}
}

// decodeWorkflowName decodes the workflow name from hex string to raw string (underlying, output)
func (x *ExecutionContext) decodeWorkflowName() string {
	bytes, err := hex.DecodeString(x.GetMetaWorkflowName())
	if err != nil {
		// This should never happen
		bytes = []byte("unknown-decode-error")
	}
	return string(bytes)
}

// This is needed to avoid issues during exporting OTel metrics to Prometheus
// For more details see https://smartcontract-it.atlassian.net/browse/INFOPLAT-1349
// ValOrUnknown returns the value if it is not empty, otherwise it returns "unknown"
func ValOrUnknown(val string) string {
	if val == "" {
		return "unknown"
	}
	return val
}

// ValShortOrUnknown returns the short len value if not empty or available, otherwise it returns "unknown"
func ValShortOrUnknown(val string, _len int) string {
	if val == "" || _len <= 0 {
		return "unknown"
	}
	if _len > len(val) {
		return val
	}
	return val[:_len]
}
