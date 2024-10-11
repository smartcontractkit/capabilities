package target

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"

	"github.com/smartcontractkit/capabilities/kvstore/kvcap"
	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
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

type KVWriteReport struct {
	keyValuePairs map[string][]byte
}

func evaluate(rawRequest capabilities.CapabilityRequest) (r KVWriteReport, err error) {
	if rawRequest.Inputs == nil {
		return r, fmt.Errorf("missing inputs field")
	}

	const signedReportField = "signedReport"
	signedReport, ok := rawRequest.Inputs.Underlying[signedReportField]
	if !ok {
		return r, fmt.Errorf("missing required field %s", signedReportField)
	}

	var inputs kvcap.WriteInputs
	if err = signedReport.UnwrapTo(&inputs.SignedReport); err != nil {
		return r, fmt.Errorf("failed to unwrap signed report: %v", err)
	}

	reportProto := &pb.Value{}
	err = proto.Unmarshal(inputs.SignedReport.Report, reportProto)
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
	c.logger.Infow("Executing",
		"WorkflowID", rawRequest.Metadata.WorkflowID,
		"WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID,
	)

	kvWriteReport, err := evaluate(rawRequest)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode signed report: %v", err)
	}
	c.logger.Debugw("Evaluated execute request",
		"WorkflowID", rawRequest.Metadata.WorkflowID,
		"WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID,
	)

	request := kvrequests.NewRequest(kvrequests.RequestParams{
		WorkflowExecutionID: rawRequest.Metadata.WorkflowExecutionID,
		ReferenceID:         rawRequest.Metadata.ReferenceID,
		Type:                kvrequests.RequestKindWrite,
		KVPairs:             kvWriteReport.keyValuePairs,
	})

	err = c.requestsStore.Add(ctx, request)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to add write request: %v", err)
	}

	timeout := time.After(60 * time.Second)
	for {
		request, err := c.requestsStore.GetByID(ctx, request.ID())
		if err != nil {
			return capabilities.CapabilityResponse{}, fmt.Errorf("failed to get request by ID: %v", err)
		}

		if request.Status == kvrequests.RequestStatusCompleted {
			response, err := values.NewMap(
				map[string]any{
					"success": true,
				},
			)
			if err != nil {
				return capabilities.CapabilityResponse{}, err
			}

			err = c.requestsStore.Remove(ctx, request.ID())
			if err != nil {
				return capabilities.CapabilityResponse{}, fmt.Errorf("failed to remove request: %v", err)
			}

			return capabilities.CapabilityResponse{
				Value: response,
			}, nil
		}

		select {
		case <-ctx.Done():
			return capabilities.CapabilityResponse{}, fmt.Errorf("request did not process, context is done")
		case <-timeout:
			return capabilities.CapabilityResponse{}, fmt.Errorf("request did not process, timeout after 60 seconds")
		case <-time.After(250 * time.Millisecond):
			c.logger.Debugw("Waiting for request to be processed",
				"RequestID", request.ID(),
			)
		}
	}
}

func (c *capability) RegisterToWorkflow(ctx context.Context, rawRequest capabilities.RegisterToWorkflowRequest) error {
	c.logger.Debugw("Registering to workflow", "WorkflowID", rawRequest.Metadata.WorkflowID)

	return nil
}

func (c *capability) UnregisterFromWorkflow(ctx context.Context, rawRequest capabilities.UnregisterFromWorkflowRequest) error {
	c.logger.Debugw("Unregistering from workflow", "WorkflowID", rawRequest.Metadata.WorkflowID)
	return nil
}
