package main

import (
	"context"
	"testing"

	_ "embed"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/wasm/host"
)

var (
	//go:embed workflow.wasm
	workflow []byte
)

func Test_Workflow(t *testing.T) {
	moduleConfig := &host.ModuleConfig{Logger: logger.Test(t), IsUncompressed: true}
	spec, err := host.GetWorkflowSpec(context.Background(), moduleConfig, workflow, []byte{})
	assert.NoError(t, err)
	assert.Equal(t, spec, false)

	m, err := values.NewMap(map[string]any{
		"ActualExecutionTime":    "2024-11-08T16:38:00.005787Z",
		"ScheduledExecutionTime": "2024-11-08T16:38:00.005787Z",
	})
	assert.NoError(t, err)

	w := BuildWorkflow([]byte{})
	fn := w.GetFn("compute")
	_, err = fn(nil, capabilities.CapabilityRequest{
		Inputs: m,
	})
	assert.NoError(t, err)

}
