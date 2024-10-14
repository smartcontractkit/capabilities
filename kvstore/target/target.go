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
	// Key values are stored with an owner prefix so that different workflows don't override each other's state
	// When the last owner workflow is unregistered, the key values are deleted
	registeredWorkflows map[string][]string
}

type Params struct {
	Logger        logger.Logger
	RequestsStore *kvrequests.RequestsStore
}

func New(p Params) *capability {
	return &capability{
		logger:              p.Logger,
		requestsStore:       p.RequestsStore,
		registeredWorkflows: make(map[string][]string),
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

	r := kvrequests.NewRequest(kvrequests.RequestParams{
		Reference: fmt.Sprintf("%s_%s", rawRequest.Metadata.WorkflowExecutionID, rawRequest.Metadata.ReferenceID),
		Type:      kvrequests.RequestTypeWrite,
		KVPairs:   kvWriteReport.keyValuePairs,
	})
	err = c.requestsStore.Add(ctx, r)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to add write request: %v", err)
	}

	timeout := time.After(60 * time.Second)
	for {
		request := c.requestsStore.GetByID(ctx, r.ID())

		if request != nil && request.Status == kvrequests.RequestStatusCompleted {
			response, err := values.NewMap(
				map[string]any{
					"success": true,
				},
			)
			if err != nil {
				return capabilities.CapabilityResponse{}, err
			}

			c.requestsStore.Remove(ctx, request.ID())

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

func (c *capability) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	_, ok := c.registeredWorkflows[request.Metadata.WorkflowID]
	if !ok {
		c.registeredWorkflows[request.Metadata.WorkflowOwner] = []string{
			request.Metadata.WorkflowID,
		}

		// c.requestsStore.AddOwnerPrefix(request.Metadata.WorkflowOwner)

		c.logger.Debugw("Added new workfow owner",
			"WorkflowID", request.Metadata.WorkflowID,
			"WorkflowOwner", request.Metadata.WorkflowOwner)
	} else {
		c.registeredWorkflows[request.Metadata.WorkflowOwner] = append(c.registeredWorkflows[request.Metadata.WorkflowOwner], request.Metadata.WorkflowID)
	}

	return nil
}

func (c *capability) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	// if c.registeredWorkflows == nil {
	// 	return fmt.Errorf("capability was incorrectly initialized")
	// }

	// workflowIDs, ok := (*c.registeredWorkflows)[request.Metadata.WorkflowOwner]
	// if !ok {
	// 	return fmt.Errorf("workflow owner not found")
	// }

	// for i, id := range workflowIDs {
	// 	if id == request.Metadata.WorkflowID {
	// 		c.registeredWorkflows[request.Metadata.WorkflowOwner] = append(workflowIDs[:i], workflowIDs[i+1:]...)
	// 		break
	// 	}
	// }

	return nil
}
