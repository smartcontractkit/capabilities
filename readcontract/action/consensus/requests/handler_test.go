package requests_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/readcontract/action/consensus/requests"
)

func TestAddObservationForRequest(t *testing.T) {
	handler, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	_, err = handler.StartConsensusRequest(ctx, "reqID1", 10)
	require.NoError(t, err)

	// Test adding a new Observation
	err = handler.AddObservationForRequest(ctx, "reqID1", 1, []byte("value1"))
	require.NoError(t, err)

	// Test adding a duplicate Observation for the same height
	err = handler.AddObservationForRequest(ctx, "reqID1", 1, []byte("value2"))
	assert.Error(t, err)

	// Test adding a valid sequential Observation
	err = handler.AddObservationForRequest(ctx, "reqID1", 2, []byte("value2"))
	require.NoError(t, err)
}

func TestGetAllRequestIDs(t *testing.T) {
	handler, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	_, err = handler.StartConsensusRequest(ctx, "reqID1", 10)
	require.NoError(t, err)
	_, err = handler.StartConsensusRequest(ctx, "reqID2", 10)
	require.NoError(t, err)

	// Add some observations
	err = handler.AddObservationForRequest(ctx, "reqID1", 1, []byte("value1"))
	require.NoError(t, err)

	err = handler.AddObservationForRequest(ctx, "reqID2", 1, []byte("value2"))
	require.NoError(t, err)

	// Get all request IDs
	ids := handler.GetAllRequestIDs()
	assert.ElementsMatch(t, ids, []string{"reqID1", "reqID2"})
}

func TestGetValueAtHeight(t *testing.T) {
	handler, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	// Test adding a new Request
	_, err = handler.StartConsensusRequest(ctx, "reqID1", 10)
	require.NoError(t, err)

	// Test adding an Observation for the Request
	err = handler.AddObservationForRequest(ctx, "reqID1", 1, []byte("value1"))
	require.NoError(t, err)

	// Test getting the value at the height
	value := handler.GetValueAtHeight("reqID1", 1)
	assert.Equal(t, []byte("value1"), value)

	// Test getting the value at a non-existent height
	value = handler.GetValueAtHeight("reqID1", 2)
	assert.Nil(t, value)
}

func TestObservationsBeforeHeightReset(t *testing.T) {
	handler, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	_, err = handler.StartConsensusRequest(ctx, "reqID1", 2)
	require.NoError(t, err)

	// Add observations
	err = handler.AddObservationForRequest(ctx, "reqID1", 1, []byte("value1"))
	require.NoError(t, err)

	err = handler.AddObservationForRequest(ctx, "reqID1", 2, []byte("value2"))
	require.NoError(t, err)

	// Add a third observation which should trigger height reset logic
	err = handler.AddObservationForRequest(ctx, "reqID1", 3, []byte("value3"))
	require.NoError(t, err)

	latestHeight := handler.GetLatestObservedHeightForRequest("reqID1")
	assert.Equal(t, uint64(3), *latestHeight)
}

func TestSetRequestValue(t *testing.T) {
	handler, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	// Test adding a new Request
	responseCh, err := handler.StartConsensusRequest(ctx, "reqID1", 10)
	require.NoError(t, err)

	handler.SetConsensusValue(ctx, "reqID1", []byte("value1"))

	response := <-responseCh

	assert.Equal(t, []byte("value1"), response)
}

func TestGetRequestsWithConsensusHeight(t *testing.T) {
	handler, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	_, err = handler.StartConsensusRequest(ctx, "reqID1", 10)
	require.NoError(t, err)

	_, err = handler.StartConsensusRequest(ctx, "reqID2", 10)
	require.NoError(t, err)

	handler.SetConsensusHeightForRequest("reqID1", 1)

	reqs := handler.GetRequestsWithConsensusHeight()
	assert.Len(t, reqs, 1)
	assert.Equal(t, "reqID1", reqs[0].RequestID)
	assert.Equal(t, uint64(1), reqs[0].Height)
}

func TestStoppedRequest(t *testing.T) {
	handler, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	// Start a new request
	_, err = handler.StartConsensusRequest(ctx, "reqID1", 10)
	require.NoError(t, err)

	// Stop the request
	handler.StopConsensusRequest(ctx, "reqID1")

	// Test AddObservationForRequest with stopped request
	err = handler.AddObservationForRequest(ctx, "reqID1", 1, []byte("value1"))
	assert.NoError(t, err)

	// Test SetConsensusHeightForRequest with stopped request
	handler.SetConsensusHeightForRequest("reqID1", 1)

	// Test GetLatestObservedHeightForRequest with stopped request
	height := handler.GetLatestObservedHeightForRequest("reqID1")
	assert.Nil(t, height)

	// Test GetValueAtHeight with stopped request
	value := handler.GetValueAtHeight("reqID1", 1)
	assert.Nil(t, value)

	// Test SetConsensusValue with stopped request
	handler.SetConsensusValue(ctx, "reqID1", []byte("value1"))
}
