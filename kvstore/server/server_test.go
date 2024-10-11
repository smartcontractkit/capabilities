package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"

	"github.com/smartcontractkit/capabilities/libs/testutils"
)

func TestNewCapabilities(t *testing.T) {
	logger := testutils.NewLogger(t)
	capabilitiesRegistry := testutils.NewCapabilitiesRegistry(t)
	capabilitiesServer := New(&loop.Server{
		Logger: logger,
	}, "kv-store-test-service")
	assert.NotNil(t, capabilitiesServer)

	// Timeout is important to avoid hanging tests
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	servicetest.RunHealthy(t, capabilitiesServer)

	assert.NoError(t, capabilitiesServer.Initialise(
		ctx,
		"",  // unused - empty config
		nil, // unused - telemetryService core.TelemetryService
		testutils.NewStore(t),
		capabilitiesRegistry,
		nil, // unused - errorLog core.ErrorLog
		nil, // unused - pipelineRunner core.PipelineRunnerService
		nil, // unused - relayerSet core.RelayerSet
		testutils.NewOracleFactory(t, logger),
	))

	capabilitiesInfos, err := capabilitiesServer.Infos(ctx)
	assert.NoError(t, err)

	assert.Len(t, capabilitiesInfos, 2)
	assert.Equal(t, "kv-store-action@1.0.0", capabilitiesInfos[0].ID)
	assert.Equal(t, "kv-store-target@1.0.0", capabilitiesInfos[1].ID)

	err = capabilitiesRegistry.Contains([]string{"kv-store-action@1.0.0", "kv-store-target@1.0.0"})
	require.NoError(t, err)

	workflow := testutils.NewWorkflow(ctx, t, []testutils.CapabilityWithConfig{
		{
			Capability: capabilitiesServer.Action,
			Config:     map[string]interface{}{},
		},
		{
			Capability: capabilitiesServer.Target,
			Config:     map[string]interface{}{},
		},
	})
	// workflow.Register(ctx)

	response, err := capabilitiesServer.Target.Execute(ctx, workflow.NewRequest(map[string]any{
		"signedReport": testutils.NewReport(t, map[string][]byte{
			"key":  []byte("value"),
			"key2": []byte("value2"),
		}),
	}))
	assert.NoError(t, err)

	assert.Equal(t, workflow.NewResponse(map[string]any{
		"success": true,
	}), response)

	// CapabilityRequest to read from the kvstore
	response, err = capabilitiesServer.Action.Execute(ctx, workflow.NewRequest(map[string]any{
		"Keys": []string{"key", "key2", "key3"},
	}))
	assert.NoError(t, err)

	assert.Equal(t, workflow.NewResponse(map[string]any{
		"key":  []byte("value"),
		"key2": []byte("value2"),
		"key3": []byte(""),
	}), response)

	response, err = capabilitiesServer.Target.Execute(ctx, workflow.NewRequest(map[string]any{
		"signedReport": testutils.NewReport(t, map[string][]byte{
			"key":  []byte(""), // Delete a key from the kvstore
			"key3": []byte("foo"),
		}),
	}))
	assert.NoError(t, err)

	assert.Equal(t, workflow.NewResponse(map[string]any{
		"success": true,
	}), response)

	// CapabilityRequest to read final values
	response, err = capabilitiesServer.Action.Execute(ctx, workflow.NewRequest(map[string]any{
		"Keys": []string{"key", "key2", "key3"},
	}))
	assert.NoError(t, err)

	assert.Equal(t, workflow.NewResponse(map[string]any{
		"key":  []byte(""),
		"key2": []byte("value2"),
		"key3": []byte("foo"),
	}), response)
}
