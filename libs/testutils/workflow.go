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

type TriggerWithConfig struct {
	Capability capabilities.TriggerCapability
	Config     map[string]interface{}
}

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
	triggers         []TriggerWithConfig
	TriggersCh       []<-chan capabilities.TriggerResponse
}

type WorkflowParams struct {
	T            *testing.T
	Capabilities []CapabilityWithConfig
	Triggers     []TriggerWithConfig
	Owner        string
}

func NewWorkflow(ctx context.Context, wp WorkflowParams) (*workflow, func(context.Context)) {
	if wp.Owner == "" {
		wp.Owner = uuid.New().String()[:32]
	}
	workflowID := uuid.New().String()[:32]

	w := workflow{
		capabilities:     wp.Capabilities,
		executionCounter: 0,
		ID:               workflowID,
		Owner:            wp.Owner,
		t:                wp.T,
		triggers:         wp.Triggers,
		TriggersCh:       make([]<-chan capabilities.TriggerResponse, len(wp.Triggers)),
	}

	w.register(ctx)
	return &w, func(ctx context.Context) {
		w.unregister(ctx)
	}
}

func (w *workflow) NewRequest(inputs map[string]any) capabilities.CapabilityRequest {
	wrapperInputs, err := values.NewMap(inputs)
	assert.NoError(w.t, err)

	w.executionCounter++
	return capabilities.CapabilityRequest{
		Metadata: capabilities.RequestMetadata{
			WorkflowOwner:       w.Owner,
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

func (w *workflow) register(ctx context.Context) {
	for triggerIndex, c := range w.triggers {
		_, err := c.Capability.Info(ctx)
		if err != nil {
			w.t.Errorf("failed to get capability info: %v", err)
		}
		config, err := values.NewMap(c.Config)
		if err != nil {
			w.t.Errorf("failed to create config map: %v", err)
		}
		r := capabilities.TriggerRegistrationRequest{
			TriggerID: fmt.Sprintf("%s-%d", w.ID, triggerIndex),
			Metadata: capabilities.RequestMetadata{
				WorkflowID:    w.ID,
				WorkflowOwner: w.Owner,
			},
			Config: config,
		}
		ch, err := c.Capability.RegisterTrigger(ctx, r)
		if err != nil {
			w.t.Errorf("failed when registering the workflow to the capability: %v", err)
		}
		w.TriggersCh[triggerIndex] = ch
	}
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

func (w *workflow) unregister(ctx context.Context) {
	for triggerIndex, c := range w.triggers {
		config, err := values.NewMap(c.Config)
		if err != nil {
			w.t.Errorf("failed to create config map: %v", err)
		}
		r := capabilities.TriggerRegistrationRequest{
			TriggerID: fmt.Sprintf("%s-%d", w.ID, triggerIndex),
			Metadata: capabilities.RequestMetadata{
				WorkflowID:    w.ID,
				WorkflowOwner: w.Owner,
			},
			Config: config,
		}
		if err = c.Capability.UnregisterTrigger(ctx, r); err != nil {
			w.t.Errorf("failed when unregistering the workflow from the trigger: %v", err)
		}
	}
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
		if err = c.Capability.UnregisterFromWorkflow(ctx, r); err != nil {
			w.t.Errorf("failed when unregistering the workflow from the capability: %v", err)
		}
	}
}
