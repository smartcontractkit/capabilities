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
	newMapFn    = values.NewMap
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
	logger logger.Logger
}

func New(p Params) capabilities.TargetCapability {
	return &capability{
		logger: p.Logger,
	}
}

func (c *capability) Info(_ context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo("beholder-target@1.0.0", capabilities.CapabilityTypeTarget, "Emits messages through beholder")
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.logger.Debug("Executing", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)

	if rawRequest.Inputs == nil {
		return capabilities.CapabilityResponse{}, errors.New("missing inputs field")
	}

	payload, err := c.payloadFromRequest(rawRequest)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	capabilityMap, err := newMapFn(payload)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	protobufMap := values.ProtoMap(capabilityMap)

	bytes, err := marshalFn(protobufMap)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	beholderClient, err := newClientFn(beholder.TestDefaultConfig())
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	if err := beholderClient.Emitter.Emit(ctx, bytes, "beholder_data_schema", "/custom-message/versions/1", // required
		"beholder_data_type", "custom_message"); err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	c.logger.Debug("Message emitted", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)

	return capabilities.CapabilityResponse{}, nil
}

func (c *capability) payloadFromRequest(rawRequest capabilities.CapabilityRequest) (map[string]any, error) {
	payload, ok := rawRequest.Inputs.Underlying["payload"]
	if !ok {
		return nil, errors.New("missing payload")
	}

	if payload == nil {
		return nil, errors.New("missing payload")
	}

	unwrappedValue, _ := payload.Unwrap()
	switch t := unwrappedValue.(type) {
	case map[string]any:
		return t, nil
	default:
		return nil, errors.New("invalid payload")
	}
}

func (c *capability) RegisterToWorkflow(_ context.Context, rawRequest capabilities.RegisterToWorkflowRequest) error {
	c.logger.Debug("Registering to workflow", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID")

	return nil
}

func (c *capability) UnregisterFromWorkflow(_ context.Context, rawRequest capabilities.UnregisterFromWorkflowRequest) error {
	c.logger.Debug("Unregistering from workflow", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID")
	return nil
}
