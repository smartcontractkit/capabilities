package action

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/capabilities/echo/shared"
	"github.com/smartcontractkit/capabilities/echo/store"
	"github.com/smartcontractkit/capabilities/echo/utils"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder/beholdertest"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func TestEchoAction(t *testing.T) {
	storageDir := t.TempDir()

	action, err := New(logger.Test(t), storageDir)
	require.NoError(t, err)
	require.NotNil(t, action)

	payload, err := anypb.New(&shared.Request{
		Key:   "test-key",
		Value: "test-value",
	})
	require.NoError(t, err)

	configPayload, err := anypb.New(&shared.Config{})
	require.NoError(t, err)

	response, err := action.Execute(context.Background(), capabilities.CapabilityRequest{
		Metadata: capabilities.RequestMetadata{
			WorkflowID:          "workflow-123",
			WorkflowExecutionID: "execution-456",
		},
		Payload:       payload,
		ConfigPayload: configPayload,
	})
	require.NoError(t, err)

	var output shared.Response
	_, err = capabilities.UnwrapResponse(response, &output)
	require.NoError(t, err)

	assert.Equal(t, "test-key", output.Key)
	assert.Equal(t, "test-value", output.Value)
	assert.Equal(t, "workflow-123:test-key:test-value", output.StoredKey)

	// Verify file was created
	expectedFile := filepath.Join(storageDir, "workflow-123_test-key_test-value.json")
	_, err = os.Stat(expectedFile)
	assert.NoError(t, err, "expected storage file to exist")

	// Clean up
	require.NoError(t, action.Close())
}

func TestEchoAction_ValidationErrors(t *testing.T) {
	storageDir := t.TempDir()

	action, err := New(logger.Test(t), storageDir)
	require.NoError(t, err)
	defer action.Close()

	tests := []struct {
		name        string
		key         string
		value       string
		expectedErr error
	}{
		{
			name:        "key contains colon",
			key:         "invalid:key",
			value:       "valid-value",
			expectedErr: utils.ErrKeyContainsColon,
		},
		{
			name:        "value contains colon",
			key:         "valid-key",
			value:       "invalid:value",
			expectedErr: utils.ErrValueContainsColon,
		},
		{
			name:        "empty key",
			key:         "",
			value:       "valid-value",
			expectedErr: utils.ErrKeyEmpty,
		},
		{
			name:        "empty value",
			key:         "valid-key",
			value:       "",
			expectedErr: utils.ErrValueEmpty,
		},
		{
			name:        "key too long",
			key:         strings.Repeat("a", 129),
			value:       "valid-value",
			expectedErr: utils.ErrKeyTooLong,
		},
		{
			name:        "value too long",
			key:         "valid-key",
			value:       strings.Repeat("a", 1029),
			expectedErr: utils.ErrValueTooLong,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := anypb.New(&shared.Request{
				Key:   tt.key,
				Value: tt.value,
			})
			require.NoError(t, err)

			configPayload, err := anypb.New(&shared.Config{})
			require.NoError(t, err)

			_, err = action.Execute(context.Background(), capabilities.CapabilityRequest{
				Metadata: capabilities.RequestMetadata{
					WorkflowID:          "workflow-123",
					WorkflowExecutionID: "execution-456",
				},
				Payload:       payload,
				ConfigPayload: configPayload,
			})
			assert.ErrorIs(t, err, tt.expectedErr)
		})
	}
}

func TestEchoAction_SnapshotUtilization(t *testing.T) {
	// Setup beholder test observer to capture emitted snapshots
	observer := beholdertest.NewObserver(t)

	// Create a fake clock to control snapshot timing
	fakeClock := clockwork.NewFakeClock()

	storageDir := t.TempDir()
	snapshotDuration := time.Second

	// Create capability with injected clock
	action, err := New(logger.Test(t), storageDir, Options{
		Clock:            fakeClock,
		SnapshotInterval: snapshotDuration,
	})
	require.NoError(t, err)
	defer action.Close()

	ctx := t.Context()

	// Helper to create and execute a request
	executeRequest := func(workflowID, workflowOwner, key, value string) {
		payload, err := anypb.New(&shared.Request{
			Key:   key,
			Value: value,
		})
		require.NoError(t, err)

		configPayload, err := anypb.New(&shared.Config{})
		require.NoError(t, err)

		_, err = action.Execute(ctx, capabilities.CapabilityRequest{
			Metadata: capabilities.RequestMetadata{
				WorkflowID:          workflowID,
				WorkflowExecutionID: "execution-1",
				WorkflowOwner:       workflowOwner,
			},
			Payload:       payload,
			ConfigPayload: configPayload,
		})
		require.NoError(t, err)
	}

	// Helper to get snapshots from beholder observer
	getSnapshots := func() []*shared.Snapshot {
		msgs := observer.Messages(t)
		var snapshots []*shared.Snapshot
		for _, msg := range msgs {
			var snap shared.Snapshot
			if err := proto.Unmarshal(msg.Body, &snap); err == nil {
				snapshots = append(snapshots, &snap)
			}
		}
		return snapshots
	}

	// Execute first request to start the resource manager
	executeRequest("workflow-1", "owner-1", "key1", "value1")

	// Give goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Step 1: Trigger first snapshot - should have 1 utilization record
	fakeClock.Advance(snapshotDuration)
	time.Sleep(10 * time.Millisecond) // Allow snapshot goroutine to process

	snapshots := getSnapshots()
	require.GreaterOrEqual(t, len(snapshots), 1, "expected at least 1 snapshot")

	lastSnapshot := snapshots[len(snapshots)-1]
	assert.Equal(t, "echo-action", lastSnapshot.Entity)
	assert.Equal(t, "echo-action-filestore", lastSnapshot.Resource)
	assert.Equal(t, "filestore", lastSnapshot.ResourceType)
	require.Len(t, lastSnapshot.Utilization, 1, "expected 1 utilization record after first execute")
	assert.Equal(t, "owner-1", lastSnapshot.Utilization[0].Owner)
	assert.Equal(t, "workflow-1", lastSnapshot.Utilization[0].WorkflowId)
	assert.Equal(t, shared.UtilizationType_UTILIZATION_TYPE_STORAGE, lastSnapshot.Utilization[0].UtilizationType)
	assert.Equal(t, "workflow-1:key1:value1", lastSnapshot.Utilization[0].ResourceId)
	assert.Greater(t, lastSnapshot.Utilization[0].Bytes, int64(200), "expected bytes > 200")

	// Step 2: Add two records for owner-2 (different workflow)
	executeRequest("workflow-2", "owner-2", "key2", "value2")
	executeRequest("workflow-2", "owner-2", "key3", "value3")

	// Trigger another snapshot
	fakeClock.Advance(snapshotDuration)
	time.Sleep(10 * time.Millisecond)

	snapshots = getSnapshots()
	require.GreaterOrEqual(t, len(snapshots), 2, "expected at least 2 snapshots")

	lastSnapshot = snapshots[len(snapshots)-1]
	require.Len(t, lastSnapshot.Utilization, 3, "expected 3 utilization records (1 for owner-1, 2 for owner-2)")

	// Verify owners and calculate bytes per owner
	ownerBytes := make(map[string]int64)
	resourceIDs := make(map[string]bool)
	for _, u := range lastSnapshot.Utilization {
		ownerBytes[u.Owner] += u.Bytes
		resourceIDs[u.ResourceId] = true
		assert.Equal(t, shared.UtilizationType_UTILIZATION_TYPE_STORAGE, u.UtilizationType)
		assert.Greater(t, u.Bytes, int64(0), "expected bytes > 0 for each record")
	}

	// Verify owner-1 has 1 record (~220 bytes)
	assert.Greater(t, ownerBytes["owner-1"], int64(200), "expected owner-1 to have ~200+ bytes")

	// Verify owner-2 has 2 records (roughly 2x owner-1)
	assert.Greater(t, ownerBytes["owner-2"], ownerBytes["owner-1"], "expected owner-2 to have more bytes than owner-1 (2 files)")

	assert.True(t, resourceIDs["workflow-1:key1:value1"], "expected resource-1 in utilization")
	assert.True(t, resourceIDs["workflow-2:key2:value2"], "expected resource-2 in utilization")
	assert.True(t, resourceIDs["workflow-2:key3:value3"], "expected resource-3 in utilization")

	// Step 3: Add a record for owner-3
	executeRequest("workflow-3", "owner-3", "key4", "value4")

	// Trigger another snapshot
	fakeClock.Advance(snapshotDuration)
	time.Sleep(10 * time.Millisecond)

	snapshots = getSnapshots()
	lastSnapshot = snapshots[len(snapshots)-1]
	require.Len(t, lastSnapshot.Utilization, 4, "expected 4 utilization records")

	// Verify all three owners with bytes tracking
	ownerBytes = make(map[string]int64)
	resourceIDs = make(map[string]bool)
	for _, u := range lastSnapshot.Utilization {
		ownerBytes[u.Owner] += u.Bytes
		resourceIDs[u.ResourceId] = true
		assert.Equal(t, shared.UtilizationType_UTILIZATION_TYPE_STORAGE, u.UtilizationType)
	}

	// owner-1 should have bytes for 1 record
	assert.Greater(t, ownerBytes["owner-1"], int64(200), "expected owner-1 to have ~200+ bytes")
	// owner-2 should have bytes for 2 records (roughly 2x owner-1)
	assert.Greater(t, ownerBytes["owner-2"], ownerBytes["owner-1"], "expected owner-2 to have more bytes than owner-1")
	// owner-3 should have bytes for 1 record
	assert.Greater(t, ownerBytes["owner-3"], int64(200), "expected owner-3 to have ~200+ bytes")

	assert.True(t, resourceIDs["workflow-1:key1:value1"], "expected resource-1")
	assert.True(t, resourceIDs["workflow-2:key2:value2"], "expected resource-2")
	assert.True(t, resourceIDs["workflow-2:key3:value3"], "expected resource-3")
	assert.True(t, resourceIDs["workflow-3:key4:value4"], "expected resource-4")

	// Total bytes should be ~890 (4 records × ~220 bytes each)
	totalBytes := ownerBytes["owner-1"] + ownerBytes["owner-2"] + ownerBytes["owner-3"]
	assert.Greater(t, totalBytes, int64(800), "expected total bytes to be ~800+")
}

// TestResourceManagerMultipleMeterables demonstrates registering multiple Meterables.
func TestResourceManagerMultipleMeterables(t *testing.T) {
	fakeClock := clockwork.NewFakeClock()
	snapshotInterval := time.Second

	// Create a mock emitter to capture snapshots
	emitter := &mockEmitter{}

	// Create ResourceManager directly
	rm := shared.NewResourceManager(logger.Test(t), shared.ResourceManagerConfig{
		SnapshotInterval: snapshotInterval,
		Clock:            fakeClock,
		Emitter:          emitter,
	})

	// Create two stores (simulating multiple resources in a capability)
	store1, err := store.New(store.StoreConfig{
		Dir: t.TempDir(),
		ResourceInfo: shared.ResourceInfo{
			Entity:       "echo-action",
			Resource:     "filestore-1",
			ResourceType: "storage_bytes",
		},
	})
	require.NoError(t, err)

	store2, err := store.New(store.StoreConfig{
		Dir: t.TempDir(),
		ResourceInfo: shared.ResourceInfo{
			Entity:       "echo-action",
			Resource:     "filestore-2",
			ResourceType: "storage_bytes",
		},
	})
	require.NoError(t, err)

	// Register both stores
	rm.Register(store1)
	rm.Register(store2)

	// Start the ResourceManager
	ctx := context.Background()
	require.NoError(t, rm.Start(ctx))
	defer rm.Close()

	// Add data to both stores
	require.NoError(t, store1.Put("key1", &store.Record{
		Key:        "key1",
		Value:      "value1",
		StoredKey:  "key1",
		WorkflowID: "wf-1",
		Owner:      "owner-1",
	}))
	require.NoError(t, store2.Put("key2", &store.Record{
		Key:        "key2",
		Value:      "value2",
		StoredKey:  "key2",
		WorkflowID: "wf-2",
		Owner:      "owner-2",
	}))

	// Advance clock to trigger snapshot
	time.Sleep(10 * time.Millisecond)
	fakeClock.Advance(snapshotInterval)
	time.Sleep(10 * time.Millisecond)

	// Should have 2 snapshots (one per registered Meterable)
	snapshots := emitter.getSnapshots()
	require.Len(t, snapshots, 2, "expected 2 snapshots (one per Meterable)")

	// Verify both resources are represented
	resources := make(map[string]bool)
	for _, s := range snapshots {
		resources[s.Resource] = true
		require.Len(t, s.Utilization, 1)
	}
	assert.True(t, resources["filestore-1"])
	assert.True(t, resources["filestore-2"])
}

// mockEmitter captures emitted snapshots for testing.
type mockEmitter struct {
	mu        sync.Mutex
	snapshots []*shared.Snapshot
}

func (m *mockEmitter) Emit(_ context.Context, data []byte) error {
	var snap shared.Snapshot
	if err := proto.Unmarshal(data, &snap); err != nil {
		return err
	}
	m.mu.Lock()
	m.snapshots = append(m.snapshots, &snap)
	m.mu.Unlock()
	return nil
}

func (m *mockEmitter) getSnapshots() []*shared.Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*shared.Snapshot, len(m.snapshots))
	copy(result, m.snapshots)
	return result
}
