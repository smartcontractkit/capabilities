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

func NewWorkflow(ctx context.Context, t *testing.T, capabilities []CapabilityWithConfig) *workflow {
	workflowOwner := uuid.New().String()[:32]
	workflowID := uuid.New().String()[:32]

	return &workflow{
		capabilities:     capabilities,
		executionCounter: 0,
		ID:               workflowID,
		Owner:            workflowOwner,
		t:                t,
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

func (w *workflow) Register(ctx context.Context) {
	for _, c := range w.capabilities {
		_, err := c.Capability.Info(ctx)
		if err != nil {
			w.t.Errorf("failed to get capability info: %v", err)
		}
		config, err := values.NewMap(c.Config)
		if err != nil {
			w.t.Errorf("failed to create config map: %v", err)
		}
		r := capabilities.RegisterToWorkflowRequest{
			Metadata: capabilities.RegistrationMetadata{
				WorkflowID:    w.ID,
				WorkflowOwner: w.Owner,
			},
			Config: config,
		}
		err = c.Capability.RegisterToWorkflow(ctx, r)
		if err != nil {
			w.t.Errorf("failed when registering the workflow to the capability: %v", err)
		}
	}
}

func (w *workflow) Unregister(ctx context.Context) {
	for _, c := range w.capabilities {
		config, err := values.NewMap(c.Config)
		if err != nil {
			w.t.Errorf("failed to create config map: %v", err)
		}
		r := capabilities.UnregisterFromWorkflowRequest{
			Metadata: capabilities.RegistrationMetadata{
				WorkflowID:    w.ID,
				WorkflowOwner: w.Owner,
			},
			Config: config,
		}
		err = c.Capability.UnregisterFromWorkflow(ctx, r)
		if err != nil {
			w.t.Errorf("failed when registering the workflow to the capability: %v", err)
		}
	}
}

// RegisterToWorkflow(ctx context.Context, request RegisterToWorkflowRequest) error
// UnregisterFromWorkflow(ctx context.Context, request UnregisterFromWorkflowRequest) error
