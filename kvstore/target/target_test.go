package target_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
	"github.com/smartcontractkit/capabilities/kvstore/target"
	"github.com/smartcontractkit/capabilities/libs/testutils"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/ocr3cap"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

func TestKVStoreTarget(t *testing.T) {
	t.Run("succeeds with valid data", func(t *testing.T) {
		t.Skip()

		logger := testutils.NewLogger(t)
		kvStore := testutils.NewStore(t)
		requestsStore, err := kvrequests.New(kvStore, logger)
		assert.NoError(t, err)

		ctx := context.Background()
		target := target.New(target.Params{
			RequestsStore: requestsStore,
			Logger:        logger,
		})

		keyValuePairs := map[string][]byte{
			"key":  []byte("value"),
			"key2": []byte("value2"),
		}
		wrappedKVPairs, err := values.Wrap(keyValuePairs)
		assert.NoError(t, err)

		keyValuePairsBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(values.Proto(wrappedKVPairs))
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

		workflow := testutils.NewWorkflow(t)
		capabilityRequest := workflow.NewRequest(map[string]any{
			"signedReport": wrappedSignedReport,
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

		writeRequestsBytes, err := kvStore.Get(ctx, kvrequests.RequestsKey)
		assert.NoError(t, err)
		var writeRequests []kvrequests.Request
		assert.NoError(t, json.Unmarshal(writeRequestsBytes, &writeRequests))

		assert.Len(t, writeRequests, 1)
		assert.Equal(t, kvrequests.Request{
			Type:                kvrequests.RequestKindWrite,
			ReferenceID:         capabilityRequest.Metadata.ReferenceID,
			WorkflowExecutionID: capabilityRequest.Metadata.WorkflowExecutionID,
			KVPairs:             keyValuePairs,
		}, writeRequests[0])
	})
}
