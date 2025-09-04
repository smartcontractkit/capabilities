package target

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/capabilities/kvstore/kvcap"
	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
)

const ID = "kv-store-target@1.0.0"

var _ capabilities.ExecutableCapability = (*kvTarget)(nil)

type kvTarget struct {
	logger        logger.Logger
	requestsStore *kvrequests.RequestsStore
}

type Params struct {
	Logger        logger.Logger
	RequestsStore *kvrequests.RequestsStore
}

func New(p Params) *kvTarget {
	return &kvTarget{
		logger:        p.Logger,
		requestsStore: p.RequestsStore,
	}
}

func (c *kvTarget) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo(ID, capabilities.CapabilityTypeTarget, "Writes KV-pairs from a SignedReport to a key-value store")
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

func (c *kvTarget) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.logger.Infow("Executing kvstore target",
		"WorkflowID", rawRequest.Metadata.WorkflowID,
		"WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID,
	)

	kvWriteReport, err := evaluate(rawRequest)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode signed report: %v", err)
	}
	c.logger.Debugw("Evaluated kvstore target execute request",
		"WorkflowID", rawRequest.Metadata.WorkflowID,
		"WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID,
	)

	r, err := kvrequests.NewRequest(kvrequests.RequestParams{
		KVPairs:   kvWriteReport.keyValuePairs,
		Namespace: rawRequest.Metadata.WorkflowOwner,
		Reference: fmt.Sprintf("%s_%s", rawRequest.Metadata.WorkflowExecutionID, rawRequest.Metadata.ReferenceID),
		Type:      kvrequests.RequestTypeWrite,
	})
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to create write request: %v", err)
	}
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

func (c *kvTarget) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	r, err := kvrequests.NewRequest(kvrequests.RequestParams{
		Namespace: request.Metadata.WorkflowOwner,
		Type:      kvrequests.RequestTypeAddNamespaceReference,
		Reference: request.Metadata.WorkflowID + "_target",
	})
	if err != nil {
		return fmt.Errorf("failed to create add namespace request: %v", err)
	}
	return c.requestsStore.Add(ctx, r)
}

func (c *kvTarget) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	r, err := kvrequests.NewRequest(kvrequests.RequestParams{
		Namespace: request.Metadata.WorkflowOwner,
		Type:      kvrequests.RequestTypeRemoveNamespaceReference,
		Reference: request.Metadata.WorkflowID + "_target",
	})
	if err != nil {
		return fmt.Errorf("failed to create remove namespace request: %v", err)
	}
	return c.requestsStore.Add(ctx, r)
}
