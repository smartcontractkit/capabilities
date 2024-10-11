package testutils

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

type CapabilityWithConfig struct {
	Capability capabilities.ExecutableCapability
	Config     map[string]interface{}
}

// workflow represents a single workflow and is used to generate capability requests
type workflow struct {
	capabilities     []CapabilityWithConfig
	executionCounter int64
	ID               string
	Owner            string
	t                *testing.T
}

type NewWorkflowParams struct {
	Capabilities []CapabilityWithConfig
	T            *testing.T
}

func NewWorkflow(ctx context.Context, params NewWorkflowParams) *workflow {
	workflowOwner := uuid.New().String()[:32]
	workflowID := uuid.New().String()[:32]
	for _, c := range params.Capabilities {
		_, err := c.Capability.Info(ctx)
		if err != nil {
			params.T.Errorf("failed to get capability info: %v", err)
		}
		config, err := values.NewMap(c.Config)
		if err != nil {
			params.T.Errorf("failed to create config map: %v", err)
		}
		r := capabilities.RegisterToWorkflowRequest{
			Metadata: capabilities.RegistrationMetadata{
				WorkflowID:    workflowID,
				WorkflowOwner: workflowOwner,
			},
			Config: config,
		}
		err = c.Capability.RegisterToWorkflow(ctx, r)
		if err != nil {
			params.T.Errorf("capability failed to register to workflow: %v", err)
		}

		// RegisterToWorkflow(ctx context.Context, request RegisterToWorkflowRequest) error
		// UnregisterFromWorkflow(ctx context.Context, request UnregisterFromWorkflowRequest) error
	}

	return &workflow{
		capabilities:     params.Capabilities,
		executionCounter: 0,
		ID:               workflowID,
		Owner:            workflowOwner,
		t:                params.T,
	}
}

func (w *workflow) NewRequest(inputs map[string]any) capabilities.CapabilityRequest {
	wrapperInputs, err := values.NewMap(inputs)
	assert.NoError(w.t, err)

	w.executionCounter++
	return capabilities.CapabilityRequest{
		Metadata: capabilities.RequestMetadata{
			WorkflowID:          w.ID,
			WorkflowExecutionID: fmt.Sprintf("%d-%s", w.executionCounter, w.ID),
			ReferenceID:         uuid.New().String()[:32],
		},
		Inputs: wrapperInputs,
	}
}

func (w *workflow) NewResponse(outputs map[string]any) capabilities.CapabilityResponse {
	wrappedOutputs, err := values.NewMap(outputs)
	assert.NoError(w.t, err)

	return capabilities.CapabilityResponse{
		Value: wrappedOutputs,
	}
}
