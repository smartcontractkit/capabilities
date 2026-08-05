package registry

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	capregv2 "github.com/smartcontractkit/chainlink-evm/gethwrappers/workflow/generated/capabilities_registry_wrapper_v2"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// fakeCaller stands in for the generated v2 contract caller.
type fakeCaller struct {
	mu sync.Mutex

	caps  []capregv2.CapabilitiesRegistryCapabilityInfo
	dons  []capregv2.CapabilitiesRegistryDONInfo
	nodes []capregv2.INodeInfoProviderNodeInfo

	capsErr  error
	donsErr  error
	nodesErr error

	calls int
	// lastStart/lastLimit record the pagination arguments actually sent.
	lastStart, lastLimit *big.Int
}

func (f *fakeCaller) GetCapabilities(_ *bind.CallOpts, start, limit *big.Int) ([]capregv2.CapabilitiesRegistryCapabilityInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastStart, f.lastLimit = start, limit
	return f.caps, f.capsErr
}

func (f *fakeCaller) GetDONs(*bind.CallOpts, *big.Int, *big.Int) ([]capregv2.CapabilitiesRegistryDONInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dons, f.donsErr
}

func (f *fakeCaller) GetNodes(*bind.CallOpts, *big.Int, *big.Int) ([]capregv2.INodeInfoProviderNodeInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nodes, f.nodesErr
}

func (f *fakeCaller) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// newTestSyncer builds a Syncer around a fake caller, bypassing NewSyncer's
// contract binding (which needs a chain backend).
func newTestSyncer(t *testing.T, caller registryCaller) *Syncer {
	t.Helper()
	s := &Syncer{
		lggr:      logger.Test(t),
		caller:    caller,
		getPeerID: func() (ragetypes.PeerID, error) { return peer(1), nil },
		interval:  time.Hour,
	}
	s.stopCh = make(chan struct{})
	s.done = make(chan struct{})
	return s
}

func fullCaller() *fakeCaller {
	p1, p2 := peer(1), peer(2)
	return &fakeCaller{
		caps: []capregv2.CapabilitiesRegistryCapabilityInfo{
			{CapabilityId: "act@1.0.0", Metadata: []byte(`{"capabilityType":1}`)},
			{CapabilityId: "cron@1.0.0", Metadata: []byte(`{"capabilityType":0}`)},
		},
		dons: []capregv2.CapabilitiesRegistryDONInfo{{
			Id:               2,
			ConfigCount:      5,
			F:                1,
			IsPublic:         true,
			AcceptsWorkflows: true,
			NodeP2PIds:       [][32]byte{p1, p2},
			DonFamilies:      []string{"zone-a"},
			Name:             "cap-don",
			Config:           []byte("don-config"),
			CapabilityConfigurations: []capregv2.CapabilitiesRegistryCapabilityConfiguration{
				{CapabilityId: "act@1.0.0", Config: []byte("act-cfg")},
			},
		}},
		nodes: []capregv2.INodeInfoProviderNodeInfo{
			{NodeOperatorId: 11, ConfigCount: 1, WorkflowDONId: 2, P2pId: p1, Signer: [32]byte{0xaa}, CapabilityIds: []string{"act@1.0.0"}},
			{NodeOperatorId: 12, ConfigCount: 1, WorkflowDONId: 2, P2pId: p2},
		},
	}
}

func TestSyncer_CurrentBeforeFirstSync(t *testing.T) {
	s := newTestSyncer(t, &fakeCaller{})

	_, err := s.Current()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not synced")

	// Health must report the missing snapshot, not "healthy with no data".
	report := s.HealthReport()
	require.Len(t, report, 1)
	for _, err := range report {
		require.Error(t, err)
	}
}

func TestSyncer_SyncBuildsSnapshot(t *testing.T) {
	ctx := context.Background()
	caller := fullCaller()
	s := newTestSyncer(t, caller)

	require.NoError(t, s.Sync(ctx))

	lr, err := s.Current()
	require.NoError(t, err)

	require.Len(t, lr.IDsToCapabilities, 2)
	assert.Equal(t, capabilities.CapabilityTypeAction, lr.IDsToCapabilities["act@1.0.0"].CapabilityType)
	assert.Equal(t, capabilities.CapabilityTypeTrigger, lr.IDsToCapabilities["cron@1.0.0"].CapabilityType)

	require.Len(t, lr.IDsToDONs, 1)
	don := lr.IDsToDONs[2]
	assert.Equal(t, "cap-don", don.Name)
	assert.Equal(t, uint8(1), don.F)
	// ConfigVersion comes from the contract's ConfigCount.
	assert.Equal(t, uint32(5), don.ConfigVersion)
	assert.Equal(t, []string{"zone-a"}, don.Families)
	assert.True(t, don.IsPublic)
	assert.True(t, don.AcceptsWorkflows)
	assert.Equal(t, []byte("don-config"), don.Config)
	require.Len(t, don.Members, 2)

	// Capability config is carried through undecoded.
	assert.Equal(t, []byte("act-cfg"), don.CapabilityConfigurations["act@1.0.0"])

	require.Len(t, lr.IDsToNodes, 2)
	assert.Equal(t, uint32(11), lr.IDsToNodes[peer(1)].NodeOperatorID)
	assert.Equal(t, [32]byte{0xaa}, lr.IDsToNodes[peer(1)].Signer)

	// A built snapshot is immediately usable end to end.
	node, err := lr.LocalNode(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), node.WorkflowDON.ID)
}

func TestSyncer_SyncSendsPagination(t *testing.T) {
	ctx := context.Background()
	caller := fullCaller()
	s := newTestSyncer(t, caller)

	require.NoError(t, s.Sync(ctx))
	assert.Equal(t, int64(0), caller.lastStart.Int64())
	assert.Equal(t, int64(pageLimit), caller.lastLimit.Int64())
}

func TestSyncer_SkipsDeprecatedAndUnparseableCapabilities(t *testing.T) {
	ctx := context.Background()

	caller := fullCaller()
	caller.caps = append(caller.caps,
		// Deprecated capabilities are still returned by the contract; treating
		// them as live would let workflows resolve a retired capability.
		capregv2.CapabilitiesRegistryCapabilityInfo{
			CapabilityId: "old@1.0.0", IsDeprecated: true, Metadata: []byte(`{"capabilityType":1}`),
		},
		// One bad metadata blob must not fail the whole sync.
		capregv2.CapabilitiesRegistryCapabilityInfo{CapabilityId: "garbage@1.0.0", Metadata: []byte("{")},
		capregv2.CapabilitiesRegistryCapabilityInfo{CapabilityId: "empty@1.0.0", Metadata: nil},
		capregv2.CapabilitiesRegistryCapabilityInfo{CapabilityId: "weird@1.0.0", Metadata: []byte(`{"capabilityType":99}`)},
	)

	s := newTestSyncer(t, caller)
	require.NoError(t, s.Sync(ctx))

	lr, err := s.Current()
	require.NoError(t, err)
	assert.Len(t, lr.IDsToCapabilities, 2)
	for _, skipped := range []string{"old@1.0.0", "garbage@1.0.0", "empty@1.0.0", "weird@1.0.0"} {
		_, ok := lr.IDsToCapabilities[skipped]
		assert.False(t, ok, "%s should have been skipped", skipped)
	}
}

func TestSyncer_SyncErrorsLeavePreviousSnapshot(t *testing.T) {
	ctx := context.Background()

	caller := fullCaller()
	s := newTestSyncer(t, caller)
	require.NoError(t, s.Sync(ctx))

	first, err := s.Current()
	require.NoError(t, err)

	// A failed read must not replace a good snapshot with a partial one; the node
	// keeps operating on the last known-good view until a read succeeds.
	caller.mu.Lock()
	caller.donsErr = errors.New("rpc down")
	caller.mu.Unlock()

	require.Error(t, s.Sync(ctx))

	second, err := s.Current()
	require.NoError(t, err)
	assert.Same(t, first, second)
}

func TestSyncer_SyncPropagatesEachReadError(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		set  func(*fakeCaller)
		want string
	}{
		{"capabilities", func(c *fakeCaller) { c.capsErr = errors.New("boom") }, "getCapabilities"},
		{"dons", func(c *fakeCaller) { c.donsErr = errors.New("boom") }, "getDONs"},
		{"nodes", func(c *fakeCaller) { c.nodesErr = errors.New("boom") }, "getNodes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := fullCaller()
			tc.set(caller)
			s := newTestSyncer(t, caller)

			err := s.Sync(ctx)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestSyncer_StartSyncsImmediately(t *testing.T) {
	ctx := context.Background()

	// A ticker first fires at T+interval, and every reader blocks until the first
	// snapshot exists, so Start must not wait a full interval.
	caller := fullCaller()
	s := newTestSyncer(t, caller)
	s.interval = time.Hour

	require.NoError(t, s.Start(ctx))
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	require.Eventually(t, func() bool {
		_, err := s.Current()
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)

	assert.GreaterOrEqual(t, caller.callCount(), 1)

	report := s.HealthReport()
	for _, err := range report {
		require.NoError(t, err)
	}
}

func TestSyncer_CloseStopsLoop(t *testing.T) {
	ctx := context.Background()

	s := newTestSyncer(t, fullCaller())
	require.NoError(t, s.Start(ctx))
	require.NoError(t, s.Close())

	// Close must be idempotent-safe under the StateMachine contract: a second
	// call reports an error rather than panicking on a closed channel.
	require.Error(t, s.Close())
}

func TestParseCapabilityType(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      []byte
		want    capabilities.CapabilityType
		wantErr bool
	}{
		{"trigger", []byte(`{"capabilityType":0}`), capabilities.CapabilityTypeTrigger, false},
		{"action", []byte(`{"capabilityType":1}`), capabilities.CapabilityTypeAction, false},
		{"consensus", []byte(`{"capabilityType":2}`), capabilities.CapabilityTypeConsensus, false},
		{"target", []byte(`{"capabilityType":3}`), capabilities.CapabilityTypeTarget, false},
		{"out of range", []byte(`{"capabilityType":9}`), capabilities.CapabilityTypeUnknown, true},
		{"empty", nil, capabilities.CapabilityTypeUnknown, true},
		{"malformed", []byte("nope"), capabilities.CapabilityTypeUnknown, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCapabilityType(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
