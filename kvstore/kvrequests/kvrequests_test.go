package kvrequests

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/capabilities/libs/testutils"
)

func TestWriteRequests(t *testing.T) {
	requestsStore := New(testutils.NewStore(t))

	ctx := context.Background()

	writeRequest := Request{
		Type:                RequestKindWrite,
		ReferenceID:         "testReferenceID",
		WorkflowExecutionID: "testID",
		KVPairs:             map[string][]byte{"key1": []byte("value1")},
	}

	err := requestsStore.Add(ctx, &writeRequest)
	assert.NoError(t, err)

	storedWriteRequestsBytes, err := requestsStore.store.Get(ctx, WriteRequestsKey)
	assert.NoError(t, err)

	var storedWriteRequests []Request
	err = json.Unmarshal(storedWriteRequestsBytes, &storedWriteRequests)
	assert.NoError(t, err)

	assert.Len(t, storedWriteRequests, 1)
	assert.Equal(t, writeRequest, storedWriteRequests[0])
}
