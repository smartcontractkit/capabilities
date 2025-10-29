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

func DistinctAttributes(in []attribute.KeyValue) []attribute.KeyValue {
	set := make(map[attribute.Key]attribute.Value, len(in))
	result := make([]attribute.KeyValue, 0, len(in))
	for _, attr := range in {
		if _, ok := set[attr.Key]; ok {
			continue
		}
		set[attr.Key] = attr.Value
		result = append(result, attr)
	}

	return result
}

func (x *ExecutionContext) MetricsAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		// Execution Context - Chain
		attribute.String("chain_family_name", ValOrUnknown(x.GetMetaChainFamilyName())),
		attribute.String("chain_id", ValOrUnknown(x.GetMetaChainId())),
		attribute.String("network_name", ValOrUnknown(x.GetMetaNetworkName())),
		attribute.String("network_name_full", ValOrUnknown(x.GetMetaNetworkNameFull())),
		attribute.Int64("workflow_don_id", int64(x.GetMetaWorkflowDonId())),
		attribute.String("capability_type", ValOrUnknown(x.GetMetaCapabilityType())),
		attribute.String("capability_id", ValOrUnknown(x.GetMetaCapabilityId())),
	}
}

// LogAttributes returns common attributes used for logging
func (x *ExecutionContext) LogAttributes() []attribute.KeyValue {
	attrs := x.MetricsAttributes()

	// Decode workflow name attribute for output
	workflowName := x.decodeWorkflowName()

	return append(attrs,
		// Execution Context - Source
		attribute.String("source_id", ValOrUnknown(x.GetMetaSourceId())),
		// Execution Context - Workflow (capabilities.RequestMetadata)
		attribute.String("workflow_id", ValOrUnknown(x.GetMetaWorkflowId())),
		attribute.String("workflow_owner", ValOrUnknown(x.GetMetaWorkflowOwner())),
		attribute.String("workflow_execution_id", x.GetMetaWorkflowExecutionId()),
		attribute.String("workflow_name", ValOrUnknown(workflowName)),
		attribute.Int64("workflow_don_config_version", int64(x.GetMetaWorkflowDonConfigVersion())),
		attribute.String("reference_id", ValOrUnknown(x.GetMetaReferenceId())),
		attribute.String("request_id", RequestID(ValOrUnknown(x.GetMetaWorkflowExecutionId()), ValOrUnknown(x.GetMetaReferenceId()))),
	)
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
