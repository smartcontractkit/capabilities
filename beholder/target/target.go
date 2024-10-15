package target

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk"
)

var (
	marshalFn   = proto.Marshal
	unmarshalFn = proto.Unmarshal
	newClientFn = beholder.NewClient
)

type Params struct {
	Logger logger.Logger
}

type Request struct {
	Metadata capabilities.RequestMetadata
	Config   *values.Map
	Inputs   sdk.CapMap
}

type capability struct {
	lggr logger.Logger
}

func New(p Params) capabilities.TargetCapability {
	return &capability{
		lggr: p.Logger,
	}
}

func (c *capability) Info(_ context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo("beholder-target@1.0.0", capabilities.CapabilityTypeTarget, "Emits messages through beholder")
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.lggr.Debugw("executing", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner)

	if rawRequest.Inputs == nil {
		c.lggr.Errorw("missing inputs field", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
		return capabilities.CapabilityResponse{}, errors.New("missing inputs field")
	}

	payload, ok := rawRequest.Inputs.Underlying["payload"]
	if !ok || payload == nil {
		c.lggr.Errorw("missing payload", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
		return capabilities.CapabilityResponse{}, errors.New("missing payload")
	}
	payloadMap, ok := payload.(*values.Map)
	if !ok {
		c.lggr.Errorw("payload not values.Map", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
		return capabilities.CapabilityResponse{}, errors.New("payload not values.Map")
	}
	pbMap := values.ProtoMap(payloadMap)

	bytes, err := marshalFn(pbMap)
	if err != nil {
		c.lggr.Errorw("error marshalling", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner, "err", err)
		return capabilities.CapabilityResponse{}, err
	}

	beholderClient, err := newClientFn(beholder.DefaultConfig())
	if err != nil {
		c.lggr.Errorw("unable to create beholder client", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner, "err", err)
		return capabilities.CapabilityResponse{}, err
	}

	if err := beholderClient.Emitter.Emit(ctx, bytes, "beholder_data_schema", "/custom-message/versions/1", // required
		"beholder_data_type", "custom_message"); err != nil {
		c.lggr.Errorw("error emitting message", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner, "err", err)
		return capabilities.CapabilityResponse{}, err
	}

	c.lggr.Debugw("message emitted", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner)

	return capabilities.CapabilityResponse{}, nil
}

func (c *capability) RegisterToWorkflow(_ context.Context, rawRequest capabilities.RegisterToWorkflowRequest) error {
	c.lggr.Debugw("registering to workflow", "workflowID", rawRequest.Metadata.WorkflowID, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
	return nil
}

func (c *capability) UnregisterFromWorkflow(_ context.Context, rawRequest capabilities.UnregisterFromWorkflowRequest) error {
	c.lggr.Debugw("unregistering from workflow", "workflowID", rawRequest.Metadata.WorkflowID, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
	return nil
}
