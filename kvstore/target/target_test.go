package target_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
	"github.com/smartcontractkit/capabilities/kvstore/target"
	"github.com/smartcontractkit/capabilities/libs/testutils"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

func TestKVStoreTarget(t *testing.T) {
	t.Run("succeeds with valid data", func(t *testing.T) {
		t.Skip()

		logger := testutils.NewLogger(t)
		requestsStore, err := kvrequests.New(logger)
		assert.NoError(t, err)

		ctx := context.Background()
		target := target.New(target.Params{
			RequestsStore: requestsStore,
			Logger:        logger,
		})

		workflow, removeWorkflow := testutils.NewWorkflow(ctx, testutils.WorkflowParams{
			T: t,
			Capabilities: []testutils.CapabilityWithConfig{
				{
					Capability: target,
				},
			},
		})
		defer removeWorkflow(ctx)

		keyValuePairs := map[string][]byte{
			"key":  []byte("value"),
			"key2": []byte("value2"),
		}
		capabilityRequest := workflow.NewRequest(map[string]any{
			"signedReport": testutils.NewReport(t, keyValuePairs),
		})
		capabilityResponse, err := target.Execute(ctx, capabilityRequest)
		assert.NoError(t, err)

		expectedValue, err := values.NewMap(map[string]any{
			"success": true,
		})
		assert.NoError(t, err)

		assert.Equal(t, capabilities.CapabilityResponse{
			Value: expectedValue,
		}, capabilityResponse)
	})
}
