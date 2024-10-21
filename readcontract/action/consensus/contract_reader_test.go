package consensus_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/smartcontractkit/capabilities/readcontract/action/consensus"
	"github.com/smartcontractkit/capabilities/readcontract/action/consensus/requests"
)

type mockResponse struct {
	values.Value
	head *types.Head
}

type mockContractReader struct {
	response mockResponse
}

func (m *mockContractReader) GetLatestValueWithHeadData(ctx context.Context, readIdentifier string, confidenceLevel primitives.ConfidenceLevel, params, returnVal any) (*types.Head, error) {
	return m.response.head, nil
}

func TestGetLatestValue(t *testing.T) {
	consensusHandler, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	contractReader := &mockContractReader{response: mockResponse{
		Value: values.NewInt64(42),
		head:  &types.Head{Height: "1"},
	}}

	clock := clockwork.NewFakeClock()
	readIdentifier := "test-read-id"
	capabilityReader := consensus.NewContractReader(contractReader, consensusHandler, readIdentifier, clock, 1*time.Second, 10)

	ctx, cancel := context.WithCancel(tests.Context(t))
	defer cancel()

	confidenceLevel := primitives.Finalized
	params := struct{}{}
	requestID := "test-request-id"

	respCh, err := capabilityReader.GetLatestValue(ctx, requestID, confidenceLevel, params)
	require.NoError(t, err)

	clock.Advance(10 * time.Second)

	// Check a request has been started on the consensus handler and get the id
	requestIDs := consensusHandler.GetAllRequestIDs()
	assert.Len(t, requestIDs, 1)

	consensusValue := values.NewInt64(42)
	valuepb := values.Proto(consensusValue)
	valueBytes, err := proto.Marshal(valuepb)
	require.NoError(t, err)

	consensusHandler.SetConsensusValue(context.Background(), requestID, valueBytes)

	resp := <-respCh

	assert.Equal(t, values.NewInt64(42), *resp.Value)

	_, open := <-respCh
	assert.False(t, open)
}

func TestDuplicateReadsAreHandled(t *testing.T) {
	contractReader := &mockContractReader{response: mockResponse{
		Value: values.NewInt64(42),
		head:  &types.Head{Height: "1"},
	}}
	consensusHandler, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	clock := clockwork.NewFakeClock()
	readIdentifier := "test-read-id"
	capabilityReader := consensus.NewContractReader(contractReader, consensusHandler, readIdentifier, clock, 1*time.Second, 10)

	ctx, cancel := context.WithCancel(tests.Context(t))
	defer cancel()

	confidenceLevel := primitives.Finalized
	params := struct{}{}
	requestID := "test-request-id"

	respCh, err := capabilityReader.GetLatestValue(ctx, requestID, confidenceLevel, params)
	require.NoError(t, err)

	clock.Advance(10 * time.Second)
	time.Sleep(100 * time.Millisecond)
	clock.Advance(10 * time.Second)
	time.Sleep(100 * time.Millisecond)

	select {
	case <-respCh:
		assert.Fail(t, "response channel should not have received a value")
	default:
	}
}

func TestContextCancelled(t *testing.T) {
	contractReader := &mockContractReader{response: mockResponse{
		Value: values.NewInt64(42),
		head:  &types.Head{Height: "1"},
	}}
	consensusHandler, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	clock := clockwork.NewFakeClock()
	readIdentifier := "test-read-id"
	capabilityReader := consensus.NewContractReader(contractReader, consensusHandler, readIdentifier, clock, 10*time.Second, 10)

	ctx, cancel := context.WithCancel(tests.Context(t))

	confidenceLevel := primitives.Finalized
	params := struct{}{}
	requestID := "test-request-id"

	respCh, err := capabilityReader.GetLatestValue(ctx, requestID, confidenceLevel, params)
	require.NoError(t, err)

	cancel()

	resp := <-respCh
	require.Error(t, resp.Err)
	assert.Equal(t, "context done", resp.Err.Error())
}
