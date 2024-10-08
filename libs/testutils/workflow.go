package testutils

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

// workflow represents a single workflow and is used to generate capability requests
type workflow struct {
	executionCounter int64
	ID               string
	t                *testing.T
}

func NewWorkflow(t *testing.T) *workflow {
	return &workflow{
		ID:               uuid.New().String()[:32],
		executionCounter: 0,
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
