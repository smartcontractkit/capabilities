package target

import (
	"context"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
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
	return capabilities.CapabilityInfo{
		ID:             "kv-store-target@1.0.0",
		CapabilityType: capabilities.CapabilityTypeTarget,
		Description:    "Writes KV-pairs from a SignedReport to a key-value store",
	}, nil
}

func success() <-chan capabilities.CapabilityResponse {
	callback := make(chan capabilities.CapabilityResponse)
	go func() {
		callback <- capabilities.CapabilityResponse{}
		close(callback)
	}()
	return callback
}

type Inputs struct {
	SignedReport string
}

type Request struct {
	Metadata capabilities.RequestMetadata
	Inputs   Inputs
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (<-chan capabilities.CapabilityResponse, error) {
	c.logger.Debugf("Executing", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)
	if err := c.store.Store(ctx, "some", []byte{1, 2, 3}); err != nil {
		return nil, err
	}
	c.logger.Debugf("Value stored", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)

	return success(), nil
}

func (c *capability) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	return nil
}

func (c *capability) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	return nil
}
