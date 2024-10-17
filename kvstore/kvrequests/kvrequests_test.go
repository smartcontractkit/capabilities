package kvrequests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/testutils"
)

var kvPairs = map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")}

func TestRequestsStore_Add(t *testing.T) {
	lggr := testutils.NewLogger(t)
	store, err := New(lggr)
	require.NoError(t, err)

	req, err := NewRequest(RequestParams{Namespace: "owner1", Type: RequestTypeRemoveNamespaceReference, Reference: "workflow_id"})
	require.NoError(t, err)
	err = store.Add(context.Background(), req)
	require.NoError(t, err)

	// Try adding the same request again
	err = store.Add(context.Background(), req)
	assert.Error(t, err)
	assert.Equal(t, "request with ID remove_namespace_reference_owner1_workflow_id already exists", err.Error())
}

func TestRequestsStore_Update(t *testing.T) {
	lggr := testutils.NewLogger(t)
	store, err := New(lggr)
	require.NoError(t, err)

	req, err := NewRequest(RequestParams{Namespace: "owner1", Type: RequestTypeWrite, KVPairs: kvPairs, Reference: "req1"})
	require.NoError(t, err)
	err = store.Add(context.Background(), req)
	require.NoError(t, err)

	updatedReq, err := NewRequest(RequestParams{Namespace: "owner1", Type: RequestTypeWrite, KVPairs: kvPairs, Reference: "req1"})
	require.NoError(t, err)
	err = store.Update(context.Background(), updatedReq)
	require.NoError(t, err)

	// Try updating a non-existent request
	nonExistentReq, err := NewRequest(RequestParams{Namespace: "owner1", Type: RequestTypeWrite, KVPairs: kvPairs, Reference: "req2"})
	require.NoError(t, err)
	err = store.Update(context.Background(), nonExistentReq)
	assert.Error(t, err)
	assert.Equal(t, "request with ID write_owner1_req2 does not exist", err.Error())
}

func TestRequestsStore_Get(t *testing.T) {
	lggr := testutils.NewLogger(t)
	store, err := New(lggr)
	require.NoError(t, err)

	req1, err := NewRequest(RequestParams{Namespace: "owner1", Type: RequestTypeWrite, Reference: "req1", KVPairs: kvPairs})
	require.NoError(t, err)
	req2, err := NewRequest(RequestParams{Namespace: "owner1", Type: RequestTypeWrite, Reference: "req2", KVPairs: kvPairs})
	require.NoError(t, err)
	req2.Status = RequestStatusCompleted
	require.NoError(t, store.Add(context.Background(), req1))
	require.NoError(t, store.Add(context.Background(), req2))

	// Get all requests
	requests, err := store.Get(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, requests, 2)

	// Get requests with specific status
	filters := &Filters{Status: RequestStatusCompleted}
	requests, err = store.Get(context.Background(), filters)
	require.NoError(t, err)
	assert.Len(t, requests, 1)
	assert.Equal(t, RequestID("write_owner1_req2"), requests[0].ID())
}

func TestRequestsStore_GetByID(t *testing.T) {
	lggr := testutils.NewLogger(t)
	store, err := New(lggr)
	require.NoError(t, err)

	req, err := NewRequest(RequestParams{
		Namespace: "owner1",
		Type:      RequestTypeWrite,
		Reference: "req1_workflow456",
		KVPairs:   kvPairs,
	})
	require.NoError(t, err)
	require.NoError(t, store.Add(context.Background(), req))

	retrievedReq := store.GetByID(context.Background(), req.ID())
	assert.NotNil(t, retrievedReq)
	assert.Equal(t, req.ID(), retrievedReq.ID())

	// Try getting a non-existent request
	retrievedReq = store.GetByID(context.Background(), "req2")
	assert.Nil(t, retrievedReq)
}

func TestRequestsStore_Remove(t *testing.T) {
	lggr := testutils.NewLogger(t)
	store, err := New(lggr)
	require.NoError(t, err)

	req, err := NewRequest(RequestParams{Namespace: "owner1", Type: RequestTypeRead, Reference: "req1", KVPairs: kvPairs})
	require.NoError(t, err)
	require.NoError(t, store.Add(context.Background(), req))

	store.Remove(context.Background(), "req1")
	retrievedReq := store.GetByID(context.Background(), "req1")
	assert.Nil(t, retrievedReq)
}
