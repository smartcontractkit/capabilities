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
)

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

func newTestSyncer(t *testing.T, reader Reader, interval time.Duration) *Syncer {
	t.Helper()
	s, err := NewSyncer(logger.Test(t), reader, func() (ragetypes.PeerID, error) { return peer(1), nil }, interval)
	require.NoError(t, err)
	return s
}

func TestNewSyncer_RequiresAReaderAndAnIdentity(t *testing.T) {
	lggr := logger.Test(t)
	getPeerID := func() (ragetypes.PeerID, error) { return peer(1), nil }

	_, err := NewSyncer(lggr, nil, getPeerID, time.Hour)
	require.ErrorContains(t, err, "reader is required")

	_, err = NewSyncer(lggr, newFakeReader(), nil, time.Hour)
	require.ErrorContains(t, err, "peer ID is required")
}

func TestSyncer_CurrentBeforeFirstSync(t *testing.T) {
	s := newTestSyncer(t, newFakeReader(), time.Hour)

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
	s := newTestSyncer(t, newFakeReader(), time.Hour)

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
	s := newTestSyncer(t, reader, time.Hour)

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
	s := newTestSyncer(t, reader, time.Hour)

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
	s := newTestSyncer(t, reader, 10*time.Millisecond)

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
