package target

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"

	"github.com/smartcontractkit/capabilities/kvstore/kvcap"
	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
	"google.golang.org/protobuf/proto"
)

var _ capabilities.TargetCapability = (*capability)(nil)

type capability struct {
	logger        logger.Logger
	requestsStore *kvrequests.RequestsStore
}

type Params struct {
	Logger        logger.Logger
	RequestsStore *kvrequests.RequestsStore
}

func New(p Params) *capability {
	return &capability{
		logger:        p.Logger,
		requestsStore: p.RequestsStore,
	}
}

func (c *capability) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo("kv-store-target@1.0.0", capabilities.CapabilityTypeTarget, "Writes KV-pairs from a SignedReport to a key-value store")
}

type ExecuteCapabilityRequest struct {
	Metadata      capabilities.RequestMetadata
	Inputs        kvcap.WriteInputs
	keyValuePairs map[string][]byte
}

func evaluate(rawRequest capabilities.CapabilityRequest) (r ExecuteCapabilityRequest, err error) {
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

	reportProto := &pb.Value{}
	err = proto.Unmarshal(r.Inputs.SignedReport.Report, reportProto)
	if err != nil {
		return r, fmt.Errorf("failed to unmarshal signed report: %v", err)
	}

	reportValue, err := values.FromProto(reportProto)
	if err != nil {
		return r, fmt.Errorf("failed to convert report proto to report value: %v", err)
	}

	err = reportValue.UnwrapTo(&r.keyValuePairs)
	if err != nil {
		return r, fmt.Errorf("failed to unwrap signed report value: %v", err)
	}

	return r, nil
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.logger.Debug("Executing",
		"WorkflowID", rawRequest.Metadata.WorkflowID,
		"WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID,
	)

	request, err := evaluate(rawRequest)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode signed report: %v", err)
	}
	c.logger.Debug("Evaluated signed report",
		"WorkflowID", request.Metadata.WorkflowID,
		"WorkflowExecutionID", request.Metadata.WorkflowExecutionID,
	)

	err = c.requestsStore.AddWriteRequest(ctx, request.Metadata.WorkflowExecutionID, request.keyValuePairs)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to set write request: %v", err)
	}
	c.logger.Debug("Stored write request",
		"WorkflowID", request.Metadata.WorkflowID,
		"WorkflowExecutionID", request.Metadata.WorkflowExecutionID,
	)

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
