package action

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/smartcontractkit/capabilities/kvstore/kvreadcap"
)

var _ capabilities.ActionCapability = (*capability)(nil)

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
	return capabilities.NewCapabilityInfo("kv-store-action@1.0.0", capabilities.CapabilityTypeTarget, "Reads values from the key-value store")
}

type Request struct {
	Metadata capabilities.RequestMetadata
	Inputs   kvreadcap.Inputs
}

type StoreMappingRequest struct {
	// Keys corresponds to the JSON schema field "keys".
	Keys []string `json:"keys" yaml:"keys" mapstructure:"keys"`

	// Values corresponds to the JSON schema field "values".
	Values [][]uint8 `json:"values" yaml:"values" mapstructure:"values"`
}

func evaluate(rawRequest capabilities.CapabilityRequest) (r Request, err error) {
	r.Metadata = rawRequest.Metadata

	if rawRequest.Inputs == nil {
		return r, fmt.Errorf("missing inputs field")
	}

	keys, ok := rawRequest.Inputs.Underlying["keys"]
	if !ok {
		return r, fmt.Errorf("missing required field %s", keys)
	}

	if err = keys.UnwrapTo(&r.Inputs.Keys); err != nil {
		return r, fmt.Errorf("failed to unwrap signed report: %v", err)
	}

	return r, nil
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.logger.Debug("Executing", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)

	request, err := evaluate(rawRequest)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode signed report: %v", err)
	}

	var vals [][]byte
	for _, key := range request.Inputs.Keys {
		v, err := c.store.Get(ctx, key)
		if err != nil {
			return capabilities.CapabilityResponse{}, err
		}
		vals = append(vals, v)
	}
	c.logger.Debug("Values stored", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)

	outputs := kvreadcap.Outputs{
		Values: vals,
	}

	valsMap, err := values.WrapMap(outputs)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	return capabilities.CapabilityResponse{
		Value: valsMap,
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
