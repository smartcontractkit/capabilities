package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/buraksezer/consistent"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/ring/internal/environment"
	"github.com/smartcontractkit/capabilities/ring/internal/pb"
	"github.com/smartcontractkit/capabilities/ring/internal/request"
	"github.com/smartcontractkit/capabilities/ring/internal/rings"
)

func TestRingPlugin_Query(t *testing.T) {
	store := requests.NewStore[*request.Request]()
	scaler := environment.NewSimpleScaler(1)
	
	hashConfig := consistent.Config{
		PartitionCount:    271,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            rings.NewHasher(),
	}

	plugin := NewRingPlugin(
		store,
		scaler,
		5*time.Minute,
		30*time.Second,
		3,
		hashConfig,
	)

	query, err := plugin.Query(context.Background(), ocr3types.OutcomeContext{})
	require.NoError(t, err)
	assert.Nil(t, query)
}

func TestRingPlugin_Observation(t *testing.T) {
	store := requests.NewStore[*request.Request]()
	scaler := environment.NewSimpleScaler(2)
	scaler.SetRingHealth(0, true)
	scaler.SetRingHealth(1, true)

	hashConfig := consistent.Config{
		PartitionCount:    271,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            rings.NewHasher(),
	}

	plugin := NewRingPlugin(
		store,
		scaler,
		5*time.Minute,
		30*time.Second,
		3,
		hashConfig,
	)

	// Add a test request to the store
	testReq := &request.Request{
		RequestID:  "test-request-1",
		Payload:    []byte("test payload"),
		ReceivedAt: time.Now(),
		ExpiresAt:  time.Now().Add(10 * time.Minute),
	}
	err := store.Add(testReq)
	require.NoError(t, err)

	ctx := context.Background()
	outctx := ocr3types.OutcomeContext{
		SeqNr:           1,
		PreviousOutcome: nil,
	}

	obs, err := plugin.Observation(ctx, outctx, nil)
	require.NoError(t, err)
	assert.NotNil(t, obs)

	// Unmarshal and validate observation
	observation := &pb.Observation{}
	err = proto.Unmarshal(obs, observation)
	require.NoError(t, err)

	assert.NotNil(t, observation.Now)
	assert.NotNil(t, observation.Status)
	assert.Equal(t, uint32(2), observation.Status.WantRings)
	assert.Len(t, observation.Hashes, 1)
	assert.Equal(t, testReq.Hash(), observation.Hashes[0])
}

func TestRingPlugin_ValidateObservation(t *testing.T) {
	store := requests.NewStore[*request.Request]()
	scaler := environment.NewSimpleScaler(1)

	hashConfig := consistent.Config{
		PartitionCount:    271,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            rings.NewHasher(),
	}

	plugin := NewRingPlugin(
		store,
		scaler,
		5*time.Minute,
		30*time.Second,
		3,
		hashConfig,
	)

	// Valid observation
	validObs := &pb.Observation{
		Status: &pb.Status{WantRings: 1},
		Now:    nil,
	}
	validObsBytes, err := proto.Marshal(validObs)
	require.NoError(t, err)

	ao := types.AttributedObservation{
		Observation: validObsBytes,
		Observer:    commontypes.OracleID(0),
	}

	err = plugin.ValidateObservation(context.Background(), ocr3types.OutcomeContext{}, nil, ao)
	assert.Error(t, err) // Should error because Now is nil
}

func TestRingPlugin_Outcome(t *testing.T) {
	store := requests.NewStore[*request.Request]()
	scaler := environment.NewSimpleScaler(1)
	scaler.SetRingHealth(0, true)

	hashConfig := consistent.Config{
		PartitionCount:    271,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            rings.NewHasher(),
	}

	plugin := NewRingPlugin(
		store,
		scaler,
		5*time.Minute,
		30*time.Second,
		1, // f=1 for testing
		hashConfig,
	)

	// Create observations from 3 nodes
	now := time.Now()
	obs1 := &pb.Observation{
		Status:  &pb.Status{WantRings: 1, Status: map[uint32]bool{0: true}},
		Now:     timestamppbNew(now),
		Hashes:  []string{"hash1", "hash2"},
	}

	obs1Bytes, err := proto.Marshal(obs1)
	require.NoError(t, err)

	aos := []types.AttributedObservation{
		{Observation: obs1Bytes, Observer: 0},
		{Observation: obs1Bytes, Observer: 1},
		{Observation: obs1Bytes, Observer: 2},
	}

	outcome, err := plugin.Outcome(context.Background(), ocr3types.OutcomeContext{}, nil, aos)
	require.NoError(t, err)
	assert.NotNil(t, outcome)

	// Unmarshal outcome
	outcomeProto := &pb.Outcome{}
	err = proto.Unmarshal(outcome, outcomeProto)
	require.NoError(t, err)

	assert.NotNil(t, outcomeProto.State)
	assert.Len(t, outcomeProto.Routes, 2) // Two hashes should be routed
}

func TestRingPlugin_Reports(t *testing.T) {
	store := requests.NewStore[*request.Request]()
	scaler := environment.NewSimpleScaler(1)

	hashConfig := consistent.Config{
		PartitionCount:    271,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            rings.NewHasher(),
	}

	plugin := NewRingPlugin(
		store,
		scaler,
		5*time.Minute,
		30*time.Second,
		3,
		hashConfig,
	)

	outcome := &pb.Outcome{
		State: &pb.RoutingState{
			Id:    1,
			State: &pb.RoutingState_RoutableRings{RoutableRings: 1},
		},
		Routes: map[string]*pb.RouteResponse{
			"hash1": {Ring: 0, ExpiresAt: timestamppbNew(time.Now().Add(5 * time.Minute))},
		},
	}

	outcomeBytes, err := proto.Marshal(outcome)
	require.NoError(t, err)

	reports, err := plugin.Reports(context.Background(), 1, outcomeBytes)
	require.NoError(t, err)
	assert.Len(t, reports, 1)
	assert.NotNil(t, reports[0].ReportWithInfo.Info)
}

// Helper function to create timestamppb.Timestamp
func timestamppbNew(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t)
}

