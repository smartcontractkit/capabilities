package action

import (
	"context"
	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/smartcontractkit/capabilities/kvstore/kvcap"
	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
)

var _ capabilities.ActionCapability = (*capability)(nil)

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
	return capabilities.NewCapabilityInfo("kv-store-action@1.0.0", capabilities.CapabilityTypeAction, "Reads keys values from a key-value store")
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
	c.logger.Debug("Executing",
		"WorkflowID", rawRequest.Metadata.WorkflowID,
		"WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID,
	)

	kvWriteReport, err := evaluate(rawRequest)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode signed report: %v", err)
	}
	c.logger.Debug("Evaluated execute request",
		"WorkflowID", rawRequest.Metadata.WorkflowID,
		"WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID,
	)

	var kvPairs = map[string][]byte{}

	for _, key := range kvWriteReport.Keys {
		kvPairs[key] = []byte{}
	}

	request := kvrequests.Request{
		WorkflowExecutionID: rawRequest.Metadata.WorkflowExecutionID,
		ReferenceID:         rawRequest.Metadata.ReferenceID,
		Type:                kvrequests.RequestKindRead,
		KVPairs:             kvPairs,
	}

	err = c.requestsStore.Add(ctx, &request)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to add read request: %v", err)
	}

	// TODO: Should probably be configurable
	timeout := time.After(60 * time.Second)
	for {
		request, err := c.requestsStore.GetByID(ctx, request.ID())
		if err != nil {
			return capabilities.CapabilityResponse{}, fmt.Errorf("failed to get request by ID: %v", err)
		}

		// TODO: Make sure response matches the JSON schema
		if request.Status == kvrequests.RequestStatusCompleted {
			var response = map[string]any{}
			for key, value := range request.KVPairs {
				response[key] = value
			}

			responseValue, err := values.NewMap(response)
			if err != nil {
				return capabilities.CapabilityResponse{}, err
			}

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
			c.logger.Debug("Waiting for request to be processed",
				"RequestID", request.ID(),
			)
		}
	}
}

func (c *capability) RegisterToWorkflow(ctx context.Context, rawRequest capabilities.RegisterToWorkflowRequest) error {
	c.logger.Debug("Registering to workflow", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID")

	return nil
}

func (c *capability) UnregisterFromWorkflow(ctx context.Context, rawRequest capabilities.UnregisterFromWorkflowRequest) error {
	c.logger.Debug("Unregistering from workflow", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID")
	return nil
}
