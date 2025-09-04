package action

import (
	"context"
	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/capabilities/kvstore/kvcap"
	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
)

const ID = "kv-store-action@1.0.0"

var _ capabilities.ExecutableCapability = (*capability)(nil)

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
	return capabilities.NewCapabilityInfo(ID, capabilities.CapabilityTypeAction, "Reads keys values from a key-value store")
}

func evaluate(rawRequest capabilities.CapabilityRequest) (*kvcap.ReadInputs, error) {
	if rawRequest.Inputs == nil {
		return nil, fmt.Errorf("missing inputs field")
	}

	var inputs kvcap.ReadInputs
	if err := rawRequest.Inputs.UnwrapTo(&inputs); err != nil {
		return nil, fmt.Errorf("failed to unwrap inputs: %v", err)
	}

	return &inputs, nil
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.logger.Infow("Executing kvstore action",
		"WorkflowID", rawRequest.Metadata.WorkflowID,
		"WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID,
	)

	kvWriteReport, err := evaluate(rawRequest)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode signed report: %v", err)
	}
	c.logger.Debugw("Evaluated kvstore action execute request",
		"WorkflowID", rawRequest.Metadata.WorkflowID,
		"WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID,
	)

	var kvPairs = map[string][]byte{}

	for _, key := range kvWriteReport.Keys {
		kvPairs[key] = []byte{}
	}

	r, err := kvrequests.NewRequest(kvrequests.RequestParams{
		KVPairs:   kvPairs,
		Namespace: rawRequest.Metadata.WorkflowOwner,
		Reference: fmt.Sprintf("%s_%s", rawRequest.Metadata.WorkflowExecutionID, rawRequest.Metadata.ReferenceID),
		Type:      kvrequests.RequestTypeRead,
	})
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to create read request: %v", err)
	}
	err = c.requestsStore.Add(ctx, r)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to add read request: %v", err)
	}

	timeout := time.After(60 * time.Second)
	for {
		request := c.requestsStore.GetByID(ctx, r.ID())

		// TODO: Make sure response matches the JSON schema
		if request != nil && request.Status == kvrequests.RequestStatusCompleted {
			var response = map[string]any{}
			for key, value := range request.KVPairs {
				response[key] = value
			}

			responseValue, err := values.NewMap(response)
			if err != nil {
				return capabilities.CapabilityResponse{}, err
			}

			c.requestsStore.Remove(ctx, request.ID())

			return capabilities.CapabilityResponse{
				Value: responseValue,
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
	r, err := kvrequests.NewRequest(kvrequests.RequestParams{
		Namespace: request.Metadata.WorkflowOwner,
		Type:      kvrequests.RequestTypeAddNamespaceReference,
		Reference: request.Metadata.WorkflowID + "_action",
	})
	if err != nil {
		return fmt.Errorf("failed to create add namespace request: %v", err)
	}
	return c.requestsStore.Add(ctx, r)
}

func (c *capability) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	r, err := kvrequests.NewRequest(kvrequests.RequestParams{
		Namespace: request.Metadata.WorkflowOwner,
		Type:      kvrequests.RequestTypeRemoveNamespaceReference,
		Reference: request.Metadata.WorkflowID + "_action",
	})
	if err != nil {
		return fmt.Errorf("failed to create remove namespace request: %v", err)
	}
	return c.requestsStore.Add(ctx, r)
}
