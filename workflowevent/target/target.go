package target

import (
	"context"
	"errors"
	"os"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk"
)

var (
	marshalFn               = proto.Marshal
	unmarshalFn             = proto.Unmarshal
	newClientFn             = beholder.NewClient
	workflowEventTargetInfo = capabilities.MustNewCapabilityInfo(
		"workflowevent-target@1.0.0",
		capabilities.CapabilityTypeTarget,
		"Emits messages through an OTEL client",
	)
	beholderDomain = "keystone"
	beholderEntity = "values"
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
	lggr           logger.Logger
	beholderClient *beholder.Client
}

func New(p Params) (capabilities.TargetCapability, error) {
	beholderClient, err := newClientFn(beholder.DefaultConfig())
	if err != nil {
		return nil, err
	}

	if domain := os.Getenv("WORKFLOW_EVENT_DOMAIN"); domain != "" {
		beholderDomain = domain
	} else {
		p.Logger.Warn("WORKFLOW_EVENT_DOMAIN not set, using default value")
	}

	if entity := os.Getenv("WORKFLOW_EVENT_ENTITY"); entity != "" {
		beholderEntity = entity
	} else {
		p.Logger.Warn("WORKFLOW_EVENT_ENTITY not set, using default value")
	}

	return &capability{
		lggr:           p.Logger,
		beholderClient: beholderClient,
	}, nil
}

func (c *capability) Info(_ context.Context) (capabilities.CapabilityInfo, error) {
	return workflowEventTargetInfo, nil
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.lggr.Debugw("executing", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner)

	if rawRequest.Inputs == nil {
		return capabilities.CapabilityResponse{}, errors.New("missing inputs field")
	}

	payload, ok := rawRequest.Inputs.Underlying["payload"]
	if !ok || payload == nil {
		return capabilities.CapabilityResponse{}, errors.New("missing payload")
	}

	payloadMap, ok := payload.(*values.Map)
	if !ok {
		return capabilities.CapabilityResponse{}, errors.New("payload is not a map")
	}

	pbMap := values.ProtoMap(payloadMap)

	bytes, err := marshalFn(pbMap)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	if err := c.beholderClient.Emitter.Emit(ctx, bytes,
		"beholder_data_schema", "/custom-message/versions/1", // required
		"beholder_data_type", "custom_message",
		"beholder_domain", beholderDomain,
		"beholder_entity", beholderEntity,
		"workflow_id", rawRequest.Metadata.WorkflowID,
		"execution_id", rawRequest.Metadata.WorkflowExecutionID,
		"workflow_name", rawRequest.Metadata.WorkflowName,
		"workflow_owner", rawRequest.Metadata.WorkflowOwner,
	); err != nil {
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
