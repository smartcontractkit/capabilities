package capability

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry/remote"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

var testGRPC = GRPCConfig{AdvertiseHost: defaultHost}

// newTestGRPCOpts makes gRPC options that register their metrics on reg. Each test must use a new
// reg. Then the test can read its own metrics.
func newTestGRPCOpts(t *testing.T, reg *prometheus.Registry) loop.GRPCOpts {
	return loop.GRPCOptsConfig{Registerer: reg}.New(logger.Test(t))
}

// TestNewRegistryDoesNotDial is the property the boot sequence rests on: the node starts this
// process and the two race, so a registry proxy that is not up yet has to delay the first lookup
// rather than fail the run.
//
// Nothing is listening on the port below, and building the registry still succeeds. Starting it waits for the node's
// registry to sync, and fails once that takes too long.
func TestNewRegistryDoesNotDial(t *testing.T) {
	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{ProxyURL: "localhost:1", CapabilityDonID: 1}, InstanceInfo{Total: 1}, testGRPC, newTestGRPCOpts(t, prometheus.NewRegistry()))

	require.NoError(t, err, "an unreachable proxy should not fail construction")
	require.NotNil(t, reg.fallback)
	assert.Empty(t, reg.added, "nothing is added yet, so nothing is announced")

	reg.syncTimeout = 2 * syncRetryInterval
	require.ErrorContains(t, reg.Start(t.Context()), "the node's registry did not sync")
}

// TestRegistryStartWaitsForTheNodeToSync checks start returns once the node's registry has synced from chain, and not
// before.
func TestRegistryStartWaitsForTheNodeToSync(t *testing.T) {
	node := newSpyRegistry(t)
	node.reg.SetRegistryMetadata(nil)
	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{ProxyURL: serveNodeRegistry(t, node)}, InstanceInfo{Total: 1}, testGRPC, newTestGRPCOpts(t, prometheus.NewRegistry()))
	require.NoError(t, err)

	synced := syncedMetadata(t, node.reg)
	time.AfterFunc(2*syncRetryInterval, func() { node.reg.SetRegistryMetadata(synced) })
	started := time.Now()
	require.NoError(t, reg.Start(t.Context()))
	assert.GreaterOrEqual(t, time.Since(started), 2*syncRetryInterval, "start waits for the node's registry to sync")
	require.NoError(t, reg.Close())
}

// TestNewRegistryDoesNotValidateTheTarget pins something worth knowing before debugging one: not
// even a nonsense target fails construction.
//
// grpc.NewClient defers everything to the first RPC, so a typo in --capabilities.proxy-url is
// reported when the registry starts and asks the node whether it has synced, after syncTimeout.
func TestNewRegistryDoesNotValidateTheTarget(t *testing.T) {
	for _, target := range []string{"!://not a target", "unknownscheme:///x"} {
		t.Run(target, func(t *testing.T) {
			reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{ProxyURL: target}, InstanceInfo{Total: 1}, testGRPC, newTestGRPCOpts(t, prometheus.NewRegistry()))
			require.NoError(t, err)
			reg.syncTimeout = syncRetryInterval
			require.Error(t, reg.Start(t.Context()), "it fails when the node's registry is asked whether it has synced")
		})
	}
}

// TestNewRegistryWithoutAProxyIsLocalOnly covers a binary with no node behind it: it dials nothing,
// and resolves only what it hosts. Its run is the only DON it knows.
func TestNewRegistryWithoutAProxyIsLocalOnly(t *testing.T) {
	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{CapabilityDonID: 1}, InstanceInfo{Total: 1}, testGRPC, newTestGRPCOpts(t, prometheus.NewRegistry()))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reg.Close()) })
	require.NoError(t, reg.Start(t.Context()))

	assert.Nil(t, reg.conn, "nothing should have been dialled")
	assert.Nil(t, reg.fallback)

	_, err = reg.DONByID(t.Context(), 1)
	require.NoError(t, err, "the run's own DON is known")
	_, err = reg.DONByID(t.Context(), 2)
	assert.Error(t, err, "there is no node to ask about any other DON")
}

// TestRegistryAddOCR3Config checks a registry with no node gives every capability the run hosts the OCR3 configs added
// to it, and that one with a node takes its config only from the node.
func TestRegistryAddOCR3Config(t *testing.T) {
	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{CapabilityDonID: 1}, InstanceInfo{Total: 1}, testGRPC,
		newTestGRPCOpts(t, prometheus.NewRegistry()))
	require.NoError(t, err)
	require.NoError(t, reg.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, reg.Close()) })
	require.NoError(t, reg.Add(t.Context(), newFake()))

	got, err := reg.ConfigForCapability(t.Context(), fakeID, 1)
	require.NoError(t, err)
	assert.Empty(t, got.Ocr3Configs, "a registry with no node has no OCR3 config until some is added")

	config := ocrtypes.ContractConfig{ConfigDigest: ocrtypes.ConfigDigest{1}, ConfigCount: 1}
	require.NoError(t, reg.AddOCR3Config("__default__", config))
	got, err = reg.ConfigForCapability(t.Context(), fakeID, 1)
	require.NoError(t, err)
	assert.Equal(t, map[string]ocrtypes.ContractConfig{"__default__": config}, got.Ocr3Configs)

	withNode, err := newRegistry(logger.Test(t), CapabilitiesConfig{ProxyURL: "localhost:1"}, InstanceInfo{Total: 1}, testGRPC,
		newTestGRPCOpts(t, prometheus.NewRegistry()))
	require.NoError(t, err)
	require.Error(t, withNode.AddOCR3Config("__default__", config))
}

// TestRegistryAddHoldsUntilClose covers the local half: this process's registry resolves what was
// added to it, and stops doing so once it closes.
func TestRegistryAddHoldsUntilClose(t *testing.T) {
	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{}, InstanceInfo{Total: 1}, testGRPC, newTestGRPCOpts(t, prometheus.NewRegistry()))
	require.NoError(t, err)
	require.NoError(t, reg.Start(t.Context()))

	require.NoError(t, reg.Add(t.Context(), newFake()))

	got, err := reg.Get(t.Context(), fakeID)
	require.NoError(t, err)
	assert.NotNil(t, got)

	// Remove hollows the registry's entry out rather than deleting it, so what the ID still maps to
	// answers nothing.
	require.NoError(t, reg.Close())

	got, err = reg.Get(t.Context(), fakeID)
	if err == nil {
		_, err = got.Info(t.Context())
	}
	require.Error(t, err, "the registry should no longer hold the capability")
}

// TestRegistryAddAnnouncesToTheNode covers the remote half: with a proxy configured, adding the
// capability announces it to the node, which can reach it back, and closing takes the announcement
// back before the connection it travelled on closes.
func TestRegistryAddAnnouncesToTheNode(t *testing.T) {
	node := newSpyRegistry(t)

	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{ProxyURL: serveNodeRegistry(t, node)}, InstanceInfo{Total: 1}, testGRPC, newTestGRPCOpts(t, prometheus.NewRegistry()))
	require.NoError(t, err)
	require.NoError(t, reg.Start(t.Context()))

	require.NoError(t, reg.Add(t.Context(), newFake()))

	assert.Equal(t, []string{fakeID}, node.addedIDs(), "the node's registry should know the capability")
	held, err := node.Get(t.Context(), fakeID)
	require.NoError(t, err)
	info, err := held.Info(t.Context())
	require.NoError(t, err, "the node should reach the capability at the address it was announced at")
	assert.Equal(t, fakeID, info.ID)

	require.NoError(t, reg.Close())
	assert.Equal(t, []string{fakeID}, node.removedIDs(), "shutdown should take the announcement back")
}

// TestRegistryDialsWithTheRunsInterceptors makes sure that calls to the node use the gRPC options.
// The client metrics must count the call.
func TestRegistryDialsWithTheRunsInterceptors(t *testing.T) {
	metrics := prometheus.NewRegistry()
	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{ProxyURL: serveNodeRegistry(t, newSpyRegistry(t))}, InstanceInfo{Total: 1}, testGRPC, newTestGRPCOpts(t, metrics))
	require.NoError(t, err)
	require.NoError(t, reg.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, reg.Close()) })

	require.NoError(t, reg.Add(t.Context(), newFake()))

	assert.Positive(t, counterTotal(t, metrics, "grpc_client_handled_total"), "the announcement should be counted")
}

// TestRegistryServesWithTheRunsInterceptors makes sure that calls from the node use the gRPC options.
// The server metrics must count the call.
func TestRegistryServesWithTheRunsInterceptors(t *testing.T) {
	node := newSpyRegistry(t)
	metrics := prometheus.NewRegistry()
	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{ProxyURL: serveNodeRegistry(t, node)}, InstanceInfo{Total: 1}, testGRPC, newTestGRPCOpts(t, metrics))
	require.NoError(t, err)
	require.NoError(t, reg.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, reg.Close()) })
	require.NoError(t, reg.Add(t.Context(), newFake()))

	held, err := node.Get(t.Context(), fakeID)
	require.NoError(t, err)
	_, err = held.Info(t.Context())
	require.NoError(t, err)

	assert.Positive(t, counterTotal(t, metrics, "grpc_server_handled_total"), "the node's call should be counted")
}

// counterTotal adds the values of all series of the counter with this name.
func counterTotal(t *testing.T, g prometheus.Gatherer, name string) float64 {
	t.Helper()
	families, err := g.Gather()
	require.NoError(t, err)

	var total float64
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			total += m.GetCounter().GetValue()
		}
	}
	return total
}

// spyRegistry is the node's registry, recording what is announced to it and what is taken back.
type spyRegistry struct {
	registry.CapabilitiesRegistry
	reg *registry.Registry

	mu      sync.Mutex
	adds    []string
	removes []string
}

// newSpyRegistry is a node's registry that has synced.
func newSpyRegistry(t *testing.T) *spyRegistry {
	reg := registry.NewRegistry(logger.Test(t))
	reg.SetRegistryMetadata(syncedMetadata(t, reg))
	return &spyRegistry{CapabilitiesRegistry: reg, reg: reg}
}

// syncedMetadata is the metadata of a node's registry that has synced, which knows the local node.
func syncedMetadata(t *testing.T, reg *registry.Registry) registry.CapabilitiesRegistryMetadata {
	t.Helper()
	m, err := newLocalMetadata(InstanceInfo{Total: 1}, 1)
	require.NoError(t, err)
	return m
}

func (s *spyRegistry) Add(ctx context.Context, c capabilities.BaseCapability) error {
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.adds = append(s.adds, info.ID)
	s.mu.Unlock()
	return s.CapabilitiesRegistry.Add(ctx, c)
}

func (s *spyRegistry) Remove(ctx context.Context, id string) error {
	s.mu.Lock()
	s.removes = append(s.removes, id)
	s.mu.Unlock()
	return s.CapabilitiesRegistry.Remove(ctx, id)
}

func (s *spyRegistry) addedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.adds...)
}

func (s *spyRegistry) removedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.removes...)
}

// serveNodeRegistry serves reg the way the node's registry proxy does, on a free port, and returns
// its address.
func serveNodeRegistry(t *testing.T, reg registry.CapabilitiesRegistry) string {
	t.Helper()
	lggr := logger.Test(t)

	transport, err := remote.NewListenerTransport(remote.ListenerTransportParams{
		Lggr:    lggr,
		Listen:  func() (net.Listener, error) { return net.Listen("tcp", "localhost:0") },
		Connect: dialer(loop.GRPCOpts{}),
	})
	require.NoError(t, err)

	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	remote.RegisterCapabilitiesRegistryServer(srv, lggr, reg, transport)
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)
	return listener.Addr().String()
}
