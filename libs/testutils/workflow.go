package testutils

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

// workflow represents a single workflow and is used to generate capability requests
type workflow struct {
	ID               string
	executionCounter int64
}

func NewWorkflow() *workflow {
	return &workflow{
		ID:               uuid.New().String()[:32],
		executionCounter: 0,
	}
}

func (w *workflow) NewRequest(inputs *values.Map) capabilities.CapabilityRequest {
	w.executionCounter++

	return capabilities.CapabilityRequest{
		Metadata: capabilities.RequestMetadata{
			WorkflowID:          w.ID,
			WorkflowExecutionID: fmt.Sprintf("%d-%s", w.executionCounter, w.ID),
			ReferenceID:         uuid.New().String()[:32],
		},
		Inputs: inputs,
	}
}
