package registry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/x/registrysyncer"
)

func peer(b byte) ragetypes.PeerID {
	var p ragetypes.PeerID
	p[0] = b
	return p
}

// fakeReader stands in for whatever the registry is actually written on.
type fakeReader struct {
	mu sync.Mutex

	snap  *Snapshot
	err   error
	reads int
	read  chan struct{}
}

func newFakeReader() *fakeReader {
	return &fakeReader{snap: fakeSnapshot("act@1.0.0"), read: make(chan struct{}, 16)}
}

func (f *fakeReader) Read(context.Context) (*Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	select {
	case f.read <- struct{}{}:
	default:
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.snap, nil
}

func (f *fakeReader) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeReader) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func fakeSnapshot(capabilityID string) *Snapshot {
	return &Snapshot{
		// AcceptsWorkflows is what makes this the node's workflow DON, which is what LocalNode
		// resolves.
		DONs: map[DonID]DON{2: {DON: capabilities.DON{
			ID: 2, Members: []ragetypes.PeerID{peer(1)}, AcceptsWorkflows: true,
		}}},
		Nodes:        map[ragetypes.PeerID]NodeInfo{peer(1): {WorkflowDONID: 2, P2pID: peer(1)}},
		Capabilities: map[string]Capability{capabilityID: {ID: capabilityID}},
	}
}

// memoryORM stands in for the table snapshots are stored in.
type memoryORM struct {
	mu     sync.Mutex
	stored []byte
	writes int
	// loadErr is what a restore fails with, which is also how "nothing stored yet" arrives.
	loadErr  error
	storeErr error
}

func newMemoryORM() *memoryORM {
	return &memoryORM{loadErr: errors.New("no rows in result set")}
}

func (m *memoryORM) AddLocalRegistry(_ context.Context, lr *LocalRegistry) error {
	if m.storeErr != nil {
		return m.storeErr
	}
	b, err := lr.MarshalJSON()
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stored = b
	m.writes++
	m.loadErr = nil
	return nil
}

func (m *memoryORM) LatestLocalRegistry(context.Context) (*LocalRegistry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	var lr LocalRegistry
	if err := lr.UnmarshalJSON(m.stored); err != nil {
		return nil, err
	}
	return &lr, nil
}

func (m *memoryORM) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writes
}

func newTestSyncer(t *testing.T, reader Reader, orm ORM, interval time.Duration) *Syncer {
	t.Helper()
	return NewSyncer(logger.Test(t), reader, orm, peer(1), interval)
}

// What a Syncer was given is checked when it starts, so that one always exists to be wired to the
// things that read from it.
func TestSyncer_StartRequiresAReaderAStoreAndAnIdentity(t *testing.T) {
	lggr := logger.Test(t)

	err := NewSyncer(lggr, nil, newMemoryORM(), peer(1), time.Hour).Start(t.Context())
	require.ErrorContains(t, err, "reader is required")

	err = NewSyncer(lggr, newFakeReader(), nil, peer(1), time.Hour).Start(t.Context())
	require.ErrorContains(t, err, "store registry snapshots is required")
}

func TestSyncer_CurrentBeforeFirstSync(t *testing.T) {
	s := newTestSyncer(t, newFakeReader(), newMemoryORM(), time.Hour)

	_, err := s.Current()
	require.ErrorContains(t, err, "not synced")

	// Health must report the missing snapshot, not "healthy with no data".
	report := s.HealthReport()
	require.Len(t, report, 1)
	for _, err := range report {
		require.Error(t, err)
	}
}

func TestSyncer_SyncPublishesWhatTheReaderReturned(t *testing.T) {
	ctx := t.Context()
	s := newTestSyncer(t, newFakeReader(), newMemoryORM(), time.Hour)

	require.NoError(t, s.Sync(ctx))

	lr, err := s.Current()
	require.NoError(t, err)
	require.Len(t, lr.IDsToCapabilities, 1)
	assert.Contains(t, lr.IDsToCapabilities, "act@1.0.0")

	// The snapshot answers "which node am I" from the identity this process supplied, which is the
	// half a reader knows nothing about.
	node, err := lr.LocalNode(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), node.WorkflowDON.ID)
}

// A registry that cannot be reached right now is better served stale than not at all: the previous
// snapshot stays, and health is what says something is wrong.
func TestSyncer_SyncErrorsLeavePreviousSnapshot(t *testing.T) {
	ctx := t.Context()
	reader := newFakeReader()
	s := newTestSyncer(t, reader, newMemoryORM(), time.Hour)

	require.NoError(t, s.Sync(ctx))
	reader.fail(errors.New("rpc down"))

	require.ErrorContains(t, s.Sync(ctx), "rpc down")

	lr, err := s.Current()
	require.NoError(t, err)
	assert.Contains(t, lr.IDsToCapabilities, "act@1.0.0")
}

func TestSyncer_StartSyncsImmediately(t *testing.T) {
	reader := newFakeReader()
	// An interval long enough that anything observed came from the initial sync rather than a tick.
	s := newTestSyncer(t, reader, newMemoryORM(), time.Hour)

	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	// The read signal fires as the read starts, so wait for the snapshot rather than for the read:
	// what matters is that starting publishes one without waiting for the first tick.
	require.Eventually(t, func() bool {
		_, err := s.Current()
		return err == nil
	}, time.Second, 5*time.Millisecond)
	assert.Equal(t, 1, reader.count())
}

func TestSyncer_CloseStopsReading(t *testing.T) {
	reader := newFakeReader()
	s := newTestSyncer(t, reader, newMemoryORM(), 10*time.Millisecond)

	require.NoError(t, s.Start(t.Context()))
	select {
	case <-reader.read:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the initial sync")
	}
	require.NoError(t, s.Close())

	after := reader.count()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, after, reader.count(), "the loop kept reading after Close")
}

// --- persistence ---

func TestSyncer_StoresWhatItReads(t *testing.T) {
	ctx := t.Context()
	orm := newMemoryORM()
	s := newTestSyncer(t, newFakeReader(), orm, time.Hour)

	require.NoError(t, s.Sync(ctx))
	assert.Equal(t, 1, orm.count(), "a successful read should have been stored")

	// What was stored is what a restart would answer from, so it has to survive the round trip.
	restored, err := orm.LatestLocalRegistry(ctx)
	require.NoError(t, err)
	assert.Contains(t, restored.IDsToCapabilities, "act@1.0.0")
	assert.Contains(t, restored.IDsToDONs, DonID(2))
	assert.Contains(t, restored.IDsToNodes, peer(1))
}

// The registry lives on a chain, and a chain is not always reachable when a process starts. Without
// the stored snapshot, a restart would fail every registry lookup until its first read landed.
func TestSyncer_ServesTheStoredSnapshotWhenTheFirstReadFails(t *testing.T) {
	ctx := t.Context()

	orm := newMemoryORM()
	stored := registrysyncer.FromSnapshot(logger.Test(t),
		func() (ragetypes.PeerID, error) { return peer(1), nil }, fakeSnapshot("stored@1.0.0"))
	require.NoError(t, orm.AddLocalRegistry(ctx, stored))

	reader := newFakeReader()
	reader.fail(errors.New("rpc down"))
	s := newTestSyncer(t, reader, orm, time.Hour)

	require.NoError(t, s.Start(ctx))
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	require.Eventually(t, func() bool {
		_, err := s.Current()
		return err == nil
	}, time.Second, 5*time.Millisecond, "nothing was published even though a snapshot was stored")

	lr, err := s.Current()
	require.NoError(t, err)
	assert.Contains(t, lr.IDsToCapabilities, "stored@1.0.0")

	// Neither the logger nor the identity survives being stored, so a restored snapshot can only
	// answer this if the syncer put them back.
	node, err := lr.LocalNode(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), node.WorkflowDON.ID)
}

// A read that cannot be stored is still a read: the process serves it, and only a restart is worse
// off for it.
func TestSyncer_StoreFailureLeavesTheSyncSuccessful(t *testing.T) {
	ctx := t.Context()

	orm := newMemoryORM()
	orm.storeErr = errors.New("disk on fire")
	s := newTestSyncer(t, newFakeReader(), orm, time.Hour)

	require.NoError(t, s.Sync(ctx))

	lr, err := s.Current()
	require.NoError(t, err)
	assert.Contains(t, lr.IDsToCapabilities, "act@1.0.0")
}
