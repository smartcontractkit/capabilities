package target_test

import (
	"context"
	"testing"

	"github.com/smartcontractkit/capabilities/kvstore/target"
	"github.com/smartcontractkit/capabilities/libs/testutils"
	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

func TestReportV1Metadata(t *testing.T) {
	t.Run("succeeds with valid data", func(t *testing.T) {
		ctx := context.Background()
		target := target.New(target.Params{
			Store:  testutils.NewStore(t),
			Logger: testutils.NewLogger(t),
		})

		workflow := testutils.NewWorkflow()
		capabilityResponse, err := target.Execute(ctx, workflow.NewRequest())
		assert.NoError(t, err)

		expectedValue, err := values.NewMap(map[string]any{
			"workflow_id": workflow.ID,
		})
		assert.NoError(t, err)

		assert.Equal(t, capabilityResponse, capabilities.CapabilityResponse{
			Value: expectedValue,
		})
	})
}
