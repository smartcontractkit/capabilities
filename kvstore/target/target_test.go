package target_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/smartcontractkit/capabilities/kvstore/target"
	"github.com/smartcontractkit/capabilities/libs/testutils"
	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/ocr3cap"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

func TestReportV1Metadata(t *testing.T) {
	t.Run("succeeds with valid data", func(t *testing.T) {
		ctx := context.Background()
		target := target.New(target.Params{
			Store:  testutils.NewStore(t),
			Logger: testutils.NewLogger(t),
		})

		keyValuePairs := map[string][]byte{
			"key":  []byte("value"),
			"key2": []byte("value2"),
		}

		keyValuePairsBytes, err := json.Marshal(keyValuePairs)
		assert.NoError(t, err)

		wrappedSignedReport, err := values.Wrap(
			ocr3cap.SignedReport{
				Context:    []uint8{},
				ID:         []uint8{1},
				Report:     keyValuePairsBytes,
				Signatures: [][]uint8{{}},
			},
		)
		assert.NoError(t, err)

		inputs, err := values.NewMap(map[string]any{
			"signedReport": wrappedSignedReport,
		})
		assert.NoError(t, err)

		workflow := testutils.NewWorkflow()
		capabilityResponse, err := target.Execute(ctx, workflow.NewRequest(inputs))
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
