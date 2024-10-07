package kvrequests

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/capabilities/libs/testutils"
)

func TestWriteRequests(t *testing.T) {
	requestsStore, err := New(testutils.NewStore(t))
	assert.NoError(t, err)

	ctx := context.Background()

	writeRequest := Request{
		Type:                RequestKindWrite,
		ReferenceID:         "testReferenceID",
		WorkflowExecutionID: "testID",
		KVPairs:             map[string][]byte{"key1": []byte("value1")},
	}

	assert.NoError(t, requestsStore.Add(ctx, &writeRequest))

	storedWriteRequestsBytes, err := requestsStore.store.Get(ctx, WriteRequestsKey)
	assert.NoError(t, err)

	var storedWriteRequests []Request
	assert.NoError(t, json.Unmarshal(storedWriteRequestsBytes, &storedWriteRequests))

	assert.Len(t, storedWriteRequests, 1)
	assert.Equal(t, writeRequest, storedWriteRequests[0])
}
