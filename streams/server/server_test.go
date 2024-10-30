package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"

	"github.com/smartcontractkit/capabilities/libs/testutils"
	"github.com/smartcontractkit/capabilities/streams/server"
	"github.com/smartcontractkit/capabilities/streams/streamscap"
)

func Test_Server(t *testing.T) {
	t.Parallel()

	t.Run("RemovingLastWorkflowClearsNamespace", func(t *testing.T) {
		logger := testutils.NewLogger(t)
		capabilitiesRegistry := testutils.NewCapabilitiesRegistry(t)
		capabilitiesServer := server.New(&loop.Server{
			Logger: logger,
		}, "kv-store-test-service")
		require.NotNil(t, capabilitiesServer)

		// Timeout is important to avoid hanging tests
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		servicetest.RunHealthy(t, capabilitiesServer)

		require.NoError(t, capabilitiesServer.Initialise(
			ctx,
			"",  // unused - empty config
			nil, // unused - telemetryService core.TelemetryService
			nil, // unused - store core.Store
			capabilitiesRegistry,
			nil, // unused - errorLog core.ErrorLog
			nil, // unused - pipelineRunner core.PipelineRunnerService
			nil, // unused - relayerSet core.RelayerSet
			nil, // unused - oracleFactory core.OracleFactory
		))

		capabilitiesInfos, err := capabilitiesServer.Infos(ctx)
		require.NoError(t, err)

		require.Len(t, capabilitiesInfos, 1)
		require.Equal(t, "mock-streams-trigger@1.0.0", capabilitiesInfos[0].ID)

		err = capabilitiesRegistry.Contains([]string{"mock-streams-trigger@1.0.0"})
		require.NoError(t, err)

		feedIDOne := streamscap.FeedId("0x0003fbba4fce42f65d6032b18aee53efdf526cc734ad296cb57565979d883bdd")
		feedIDTwo := streamscap.FeedId("0x0003c317fec7fad514c67aacc6366bf2f007ce37100e3cddcacd0ccaa1f3746d")
		workflow, removeWorkflow := testutils.NewWorkflow(ctx, testutils.WorkflowParams{
			T: t,
			Triggers: []testutils.TriggerWithConfig{
				{
					Capability: capabilitiesServer.Trigger,
					Config: map[string]interface{}{
						"FeedIds":        []streamscap.FeedId{feedIDOne, feedIDTwo},
						"MaxFrequencyMs": 100,
					},
				},
			},
		})
		defer removeWorkflow(ctx)

		// Wait for the first 3 events
		for i := 0; i < 3; i++ {
			select {
			case response := <-workflow.TriggersCh[0]:
				require.NotNil(t, response)

				require.NoError(t, response.Err)
				require.Equal(t, "mock-streams-trigger@1.0.0", response.Event.TriggerType)

				output := &streamscap.Feed{}
				err := response.Event.Outputs.UnwrapTo(output)
				require.NoError(t, err)

				require.Equal(t, 2, len(output.Payload))
				require.Equal(t, feedIDOne, output.Payload[0].FeedID)
				require.Equal(t, feedIDTwo, output.Payload[1].FeedID)
			case <-ctx.Done():
				t.Fatal("timeout waiting for event")
			}
		}
	})
}
