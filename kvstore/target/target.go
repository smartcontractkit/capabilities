package target

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/smartcontractkit/capabilities/kvstore/kvcap"
)

var _ capabilities.TargetCapability = (*capability)(nil)

type capability struct {
	logger logger.Logger
	store  core.KeyValueStore
}

type Params struct {
	Logger logger.Logger
	Store  core.KeyValueStore
}

func New(p Params) *capability {
	return &capability{
		logger: p.Logger,
		store:  p.Store,
	}
}

func (c *capability) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo("kv-store-target@1.0.0", capabilities.CapabilityTypeTarget, "Writes KV-pairs from a SignedReport to a key-value store")
}

type ExecuteRequest struct {
	Metadata capabilities.RequestMetadata
	Inputs   kvcap.Inputs
}

func evaluate(rawRequest capabilities.CapabilityRequest) (r ExecuteRequest, err error) {
	r.Metadata = rawRequest.Metadata

	if rawRequest.Inputs == nil {
		return r, fmt.Errorf("missing inputs field")
	}

	const signedReportField = "signedReport"
	signedReport, ok := rawRequest.Inputs.Underlying[signedReportField]
	if !ok {
		return r, fmt.Errorf("missing required field %s", signedReportField)
	}

	if err = signedReport.UnwrapTo(&r.Inputs.SignedReport); err != nil {
		return r, fmt.Errorf("failed to unwrap signed report: %v", err)
	}

	return r, nil
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.logger.Debug("Executing", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)

	request, err := evaluate(rawRequest)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode signed report: %v", err)
	}
	c.logger.Debug("Evaluated signed report", "WorkflowID", request.Metadata.WorkflowID, "WorkflowExecutionID", request.Metadata.WorkflowExecutionID, "ReportVersion", request.Inputs.SignedReport)

	// abi.encode to something
	// Can we do bytes for identical consensus / no-op encoder? protos.Value?
	// // TODO: Decode request.Inputs.SignedReport.Report into KV pairs
	// setRequest := NewWriteRequest(request.Metadata.WorkflowExecutionID, map[string][]byte{
	// 	"for": []byte("bar"),
	// })

	var keyValuePairs map[string][]byte

	if err = json.Unmarshal(request.Inputs.SignedReport.Report, &keyValuePairs); err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to unmarshal signed report: %v", err)
	}

	for k, v := range keyValuePairs {
		if err = c.store.Store(ctx, k, v); err != nil {
			return capabilities.CapabilityResponse{}, err
		}
		c.logger.Debug("Value stored", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID, "Key", k, "Value", v)
	}

	if err = c.store.Store(ctx, "some", []byte{1, 2, 3}); err != nil {
		return capabilities.CapabilityResponse{}, err
	}
	c.logger.Debug("Value stored", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)

	response, err := values.NewMap(
		map[string]any{
			"success": true,
		},
	)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	return capabilities.CapabilityResponse{
		Value: response,
	}, nil
}

func (c *capability) RegisterToWorkflow(ctx context.Context, rawRequest capabilities.RegisterToWorkflowRequest) error {
	c.logger.Debug("Registering to workflow", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID")

	return nil
}

func (c *capability) UnregisterFromWorkflow(ctx context.Context, rawRequest capabilities.UnregisterFromWorkflowRequest) error {
	c.logger.Debug("Unregistering from workflow", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID")
	return nil
}
