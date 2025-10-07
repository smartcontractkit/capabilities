package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/kvstore/server"
	"github.com/smartcontractkit/capabilities/libs/testutils"
)

func Test_Server(t *testing.T) {
	t.Parallel()

	t.Run("RemovingLastWorkflowClearsNamespace", func(t *testing.T) {
		logger := testutils.NewLogger(t)
		capabilitiesRegistry := testutils.NewCapabilitiesRegistry(t)
		capabilitiesServer := server.New(logger)
		require.NotNil(t, capabilitiesServer)

		// Timeout is important to avoid hanging tests
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		servicetest.RunHealthy(t, capabilitiesServer)

		require.NoError(t, capabilitiesServer.Initialise(ctx, core.StandardCapabilitiesDependencies{
			Store:              testutils.NewStore(t),
			CapabilityRegistry: capabilitiesRegistry,
			OracleFactory:      testutils.NewOracleFactory(t, logger),
		}))

		capabilitiesInfos, err := capabilitiesServer.Infos(ctx)
		require.NoError(t, err)

		require.Len(t, capabilitiesInfos, 2)
		require.Equal(t, "kv-store-action@1.0.0", capabilitiesInfos[0].ID)
		require.Equal(t, "kv-store-target@1.0.0", capabilitiesInfos[1].ID)

		err = capabilitiesRegistry.Contains([]string{"kv-store-action@1.0.0", "kv-store-target@1.0.0"})
		require.NoError(t, err)

		workflow, removeWorkflow := testutils.NewWorkflow(ctx, testutils.WorkflowParams{
			T: t,
			Capabilities: []testutils.CapabilityWithConfig{
				{
					Capability: capabilitiesServer.Action,
				},
				{
					Capability: capabilitiesServer.Target,
				},
			},
			Owner: "owner1",
		})

		response, err := capabilitiesServer.Target.Execute(ctx, workflow.NewRequest(map[string]any{
			"signedReport": testutils.NewReport(t, map[string][]byte{
				"key":  []byte("value"),
				"key2": []byte("value2"),
			}),
		}))
		require.NoError(t, err)

		require.Equal(t, workflow.NewResponse(map[string]any{
			"success": true,
		}), response)

		// CapabilityRequest to read from the kvstore
		response, err = capabilitiesServer.Action.Execute(ctx, workflow.NewRequest(map[string]any{
			"Keys": []string{"key", "key2", "key3"},
		}))
		require.NoError(t, err)

		require.Equal(t, workflow.NewResponse(map[string]any{
			"key":  []byte("value"),
			"key2": []byte("value2"),
			"key3": []byte(""),
		}), response)
		removeWorkflow(ctx)

		// CapabilityRequest to read final values
		response, err = capabilitiesServer.Action.Execute(ctx, workflow.NewRequest(map[string]any{
			"Keys": []string{"key", "key2", "key3"},
		}))
		require.NoError(t, err)

		require.Equal(t, workflow.NewResponse(map[string]any{
			"key":  []byte(""),
			"key2": []byte(""),
			"key3": []byte(""),
		}), response)
	})

	t.Run("MultipleNamespaces", func(t *testing.T) {
		logger := testutils.NewLogger(t)
		capabilitiesRegistry := testutils.NewCapabilitiesRegistry(t)
		capabilitiesServer := server.New(logger)
		require.NotNil(t, capabilitiesServer)

		// Timeout is important to avoid hanging tests
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		servicetest.RunHealthy(t, capabilitiesServer)

		require.NoError(t, capabilitiesServer.Initialise(ctx, core.StandardCapabilitiesDependencies{
			Store:              testutils.NewStore(t),
			CapabilityRegistry: capabilitiesRegistry,
			OracleFactory:      testutils.NewOracleFactory(t, logger),
		}))

		capabilitiesInfos, err := capabilitiesServer.Infos(ctx)
		require.NoError(t, err)

		require.Len(t, capabilitiesInfos, 2)
		require.Equal(t, "kv-store-action@1.0.0", capabilitiesInfos[0].ID)
		require.Equal(t, "kv-store-target@1.0.0", capabilitiesInfos[1].ID)

		err = capabilitiesRegistry.Contains([]string{"kv-store-action@1.0.0", "kv-store-target@1.0.0"})
		require.NoError(t, err)

		workflow1, removeWorkflow1 := testutils.NewWorkflow(ctx, testutils.WorkflowParams{
			T: t,
			Capabilities: []testutils.CapabilityWithConfig{
				{
					Capability: capabilitiesServer.Action,
				},
				{
					Capability: capabilitiesServer.Target,
				},
			},
			Owner: "owner1",
		})
		defer removeWorkflow1(ctx)

		workflow2, removeWorkflow2 := testutils.NewWorkflow(ctx, testutils.WorkflowParams{
			T: t,
			Capabilities: []testutils.CapabilityWithConfig{
				{
					Capability: capabilitiesServer.Action,
				},
				{
					Capability: capabilitiesServer.Target,
				},
			},
			Owner: "owner2",
		})
		defer removeWorkflow2(ctx)

		response1, err := capabilitiesServer.Target.Execute(ctx, workflow1.NewRequest(map[string]any{
			"signedReport": testutils.NewReport(t, map[string][]byte{
				"key": []byte("foo"),
			}),
		}))
		require.NoError(t, err)

		require.Equal(t, workflow1.NewResponse(map[string]any{
			"success": true,
		}), response1)

		response2, err := capabilitiesServer.Target.Execute(ctx, workflow2.NewRequest(map[string]any{
			"signedReport": testutils.NewReport(t, map[string][]byte{
				"key": []byte("bar"),
			}),
		}))
		require.NoError(t, err)

		require.Equal(t, workflow2.NewResponse(map[string]any{
			"success": true,
		}), response2)

		// READ WORKFLOW 1
		response1, err = capabilitiesServer.Action.Execute(ctx, workflow1.NewRequest(map[string]any{
			"Keys": []string{"key"},
		}))
		require.NoError(t, err)

		require.Equal(t, workflow1.NewResponse(map[string]any{
			"key": []byte("foo"),
		}), response1)

		// READ WORKFLOW 2
		response2, err = capabilitiesServer.Action.Execute(ctx, workflow2.NewRequest(map[string]any{
			"Keys": []string{"key"},
		}))
		require.NoError(t, err)

		require.Equal(t, workflow2.NewResponse(map[string]any{
			"key": []byte("bar"),
		}), response2)
	})

	t.Run("PreserveNamespaceIfOnlySomeWorkflowsAreRemoved", func(t *testing.T) {
		logger := testutils.NewLogger(t)
		capabilitiesRegistry := testutils.NewCapabilitiesRegistry(t)
		capabilitiesServer := server.New(logger)
		require.NotNil(t, capabilitiesServer)

		// Timeout is important to avoid hanging tests
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		servicetest.RunHealthy(t, capabilitiesServer)

		require.NoError(t, capabilitiesServer.Initialise(ctx, core.StandardCapabilitiesDependencies{
			Store:              testutils.NewStore(t),
			CapabilityRegistry: capabilitiesRegistry,
			OracleFactory:      testutils.NewOracleFactory(t, logger),
		}))

		capabilitiesInfos, err := capabilitiesServer.Infos(ctx)
		require.NoError(t, err)

		require.Len(t, capabilitiesInfos, 2)
		require.Equal(t, "kv-store-action@1.0.0", capabilitiesInfos[0].ID)
		require.Equal(t, "kv-store-target@1.0.0", capabilitiesInfos[1].ID)

		err = capabilitiesRegistry.Contains([]string{"kv-store-action@1.0.0", "kv-store-target@1.0.0"})
		require.NoError(t, err)

		workflow1, removeWorkflow1 := testutils.NewWorkflow(ctx, testutils.WorkflowParams{
			T: t,
			Capabilities: []testutils.CapabilityWithConfig{
				{
					Capability: capabilitiesServer.Target,
				},
			},
			Owner: "owner1",
		})

		workflow2, removeWorkflow2 := testutils.NewWorkflow(ctx, testutils.WorkflowParams{
			T: t,
			Capabilities: []testutils.CapabilityWithConfig{
				{
					Capability: capabilitiesServer.Action,
				},
			},
			Owner: "owner1",
		})
		defer removeWorkflow2(ctx)

		// WRITE with workflow 1
		response, err := capabilitiesServer.Target.Execute(ctx, workflow1.NewRequest(map[string]any{
			"signedReport": testutils.NewReport(t, map[string][]byte{
				"key": []byte("foo"),
			}),
		}))
		require.NoError(t, err)
		require.Equal(t, workflow1.NewResponse(map[string]any{
			"success": true,
		}), response)

		removeWorkflow1(ctx)

		// READ with workflow 2
		response, err = capabilitiesServer.Action.Execute(ctx, workflow2.NewRequest(map[string]any{
			"Keys": []string{"key"},
		}))
		require.NoError(t, err)

		require.Equal(t, workflow1.NewResponse(map[string]any{
			"key": []byte("foo"),
		}), response)
	})
}
