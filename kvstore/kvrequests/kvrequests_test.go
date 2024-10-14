package kvrequests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/capabilities/libs/testutils"
)

func TestRequestsStore_Add(t *testing.T) {
	lggr := testutils.NewLogger(t)
	store, err := New(lggr)
	assert.NoError(t, err)

	req := &Request{Reference: "req1", Status: RequestStatusPending}
	err = store.Add(context.Background(), req)
	assert.NoError(t, err)

	// Try adding the same request again
	err = store.Add(context.Background(), req)
	assert.Error(t, err)
	assert.Equal(t, "request with ID write_req1 already exists", err.Error())
}

func TestRequestsStore_Update(t *testing.T) {
	lggr := testutils.NewLogger(t)
	store, err := New(lggr)
	assert.NoError(t, err)

	req := &Request{Reference: "req1", Status: RequestStatusPending}
	err = store.Add(context.Background(), req)
	assert.NoError(t, err)

	updatedReq := &Request{Reference: "req1", Status: RequestStatusCompleted}
	err = store.Update(context.Background(), updatedReq)
	assert.NoError(t, err)

	// Try updating a non-existent request
	nonExistentReq := &Request{Reference: "req2", Status: RequestStatusCompleted}
	err = store.Update(context.Background(), nonExistentReq)
	assert.Error(t, err)
	assert.Equal(t, "request with ID write_req2 does not exist", err.Error())
}

func TestRequestsStore_Get(t *testing.T) {
	lggr := testutils.NewLogger(t)
	store, err := New(lggr)
	assert.NoError(t, err)

	req1 := &Request{Reference: "req1", Status: RequestStatusPending}
	req2 := &Request{Reference: "req2", Status: RequestStatusCompleted}

	assert.NoError(t, store.Add(context.Background(), req1))
	assert.NoError(t, store.Add(context.Background(), req2))

	// Get all requests
	requests, err := store.Get(context.Background(), nil)
	assert.NoError(t, err)
	assert.Len(t, requests, 2)

	// Get requests with specific status
	filters := &Filters{Status: RequestStatusPending}
	requests, err = store.Get(context.Background(), filters)
	assert.NoError(t, err)
	assert.Len(t, requests, 1)
	assert.Equal(t, RequestID("write_req1"), requests[0].ID())
}

func TestRequestsStore_GetByID(t *testing.T) {
	lggr := testutils.NewLogger(t)
	store, err := New(lggr)
	assert.NoError(t, err)

	req := NewRequest(RequestParams{
		Type:      RequestTypeWrite,
		Reference: "req1_workflow456",
		KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
	})
	assert.NoError(t, store.Add(context.Background(), req))

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
	assert.NoError(t, err)

	req := &Request{Reference: "req1", Status: RequestStatusPending}
	assert.NoError(t, store.Add(context.Background(), req))

	store.Remove(context.Background(), "req1")
	retrievedReq := store.GetByID(context.Background(), "req1")
	assert.Nil(t, retrievedReq)
}
