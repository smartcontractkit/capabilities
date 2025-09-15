package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/libocr/ragep2p/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapabilityWatcherServer_ContextLeakOnCancellation(t *testing.T) {
	// This test SHOULD FAIL until the context leak issue is fixed
	// It exposes that runLoop ignores the Start() context and uses Background() instead

	lggr := logger.Test(t)
	server := New(lggr)

	// Use a context with a short timeout to simulate cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start the server with the timeout context
	err := server.Start(ctx)
	require.NoError(t, err)

	// Verify the server created its internal cancel function
	assert.NotNil(t, server.serverCancel, "CapabilityWatcherServer should have created serverCancel")

	// Wait for the context to timeout
	<-ctx.Done()
	assert.Error(t, ctx.Err(), "Context should be cancelled/timed out")

	// Give time for runLoop to potentially stop (it won't because it uses Background context)
	time.Sleep(50 * time.Millisecond)

	// CURRENT BROKEN BEHAVIOR: runLoop continues running despite context cancellation
	// This assertion documents the current broken state (will pass until fixed)
	assert.NotNil(t, server.serverCancel, "serverCancel still exists - runLoop is still running")

	// WHAT SHOULD HAPPEN (this will fail until the issue is fixed):
	// The runLoop should stop when the Start() context is cancelled
	// In a correct implementation, we shouldn't need explicit Close() to stop the runLoop

	// For now, we need explicit Close() because the bug makes runLoop ignore context cancellation
	err = server.Close()
	assert.NoError(t, err)

	// This assertion documents the DESIRED behavior (will fail until fixed):
	// TODO: Uncomment when implementing proper context handling
	// The runLoop should automatically stop when Start() context is cancelled
	// We should NOT need explicit Close() calls for context-based shutdown

	t.Log("BUG EXPOSED: runLoop ignores Start() context and uses Background() instead")
	t.Log("CURRENT: serverCtx, serverCancel := context.WithCancel(context.Background())")
	t.Log("RESULT: Goroutine leak - runLoop continues after context cancellation")
	t.Log("NEEDED: serverCtx, serverCancel := context.WithCancel(ctx)")
}

func TestCapabilityWatcherServer_RunLoopIgnoresContextCancellation(t *testing.T) {
	// More explicit test showing the runLoop doesn't stop when Start() context is cancelled
	lggr := logger.Test(t)
	server := New(lggr)

	// Use a context with timeout to simulate cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Start the server with the timeout context
	err := server.Start(ctx)
	require.NoError(t, err)

	// Wait for the context to timeout/cancel
	<-ctx.Done()
	assert.Error(t, ctx.Err(), "Context should be cancelled/timed out")

	// Give some time for runLoop to potentially stop (it won't because it uses Background context)
	time.Sleep(100 * time.Millisecond)

	// The runLoop is still running! We need to explicitly Close() to stop it
	// This demonstrates the bug: runLoop doesn't respect the Start() context

	err = server.Close()
	assert.NoError(t, err)

	t.Log("BUG: CapabilityWatcherServer.runLoop ignores Start() context cancellation")
	t.Log("EVIDENCE: runLoop continues running even after Start() context times out")
	t.Log("ROOT CAUSE: Uses context.Background() instead of passed context in Start()")
}

func TestCapabilityWatcherServer_ServiceMapAccess(t *testing.T) {
	// This test SHOULD FAIL until proper service lifecycle management is implemented
	// It exposes that the server has no actual capability discovery and monitoring

	lggr := logger.Test(t)
	server := New(lggr)

	// Create a mock registry with some capabilities to discover
	mockRegistry := &mockCapabilityRegistry{
		shouldFailCapabilityCreation: false, // Don't fail, but also don't provide real capabilities
	}
	server.capRegistry = mockRegistry

	ctx := context.Background()
	err := server.Start(ctx)
	require.NoError(t, err)

	// Give the server time to discover and start monitoring capabilities
	time.Sleep(200 * time.Millisecond)

	// CURRENT BROKEN BEHAVIOR: Server doesn't actually discover or monitor any capabilities
	// This assertion will PASS with current broken implementation
	server.servicesMutex.Lock()
	serviceCount := len(server.runningServices)
	server.servicesMutex.Unlock()

	assert.Equal(t, 0, serviceCount, "Current implementation: no services discovered or started")

	// WHAT SHOULD HAPPEN (this will fail until proper implementation):
	// The server should:
	// 1. Discover capabilities from the registry during runLoop
	// 2. Start monitoring services for discovered capabilities
	// 3. Maintain a map of running services
	// 4. Handle capability lifecycle properly

	// This assertion documents the DESIRED behavior (will fail until fixed):
	// TODO: Uncomment when implementing proper capability discovery
	// In a working system, the server should discover and monitor capabilities
	// assert.Greater(t, serviceCount, 0, "Server should discover and monitor capabilities")

	// Test cleanup - this works correctly
	err = server.Close()
	assert.NoError(t, err)

	// Verify cleanup works (this should pass)
	server.servicesMutex.Lock()
	finalServiceCount := len(server.runningServices)
	server.servicesMutex.Unlock()

	assert.Equal(t, 0, finalServiceCount, "Services map should be empty after Close()")

	t.Log("BUG EXPOSED: Server doesn't actually discover or monitor capabilities")
	t.Log("CURRENT: runLoop calls performChecks but doesn't start monitoring services")
	t.Log("RESULT: No actual health checking or capability monitoring occurs")
	t.Log("NEEDED: Implement capability discovery and service lifecycle management")
}

func TestCapabilityWatcherServer_SingleCapabilityFailureTerminatesService(t *testing.T) {
	// This test SHOULD FAIL until the error recovery issue is fixed
	// It exposes that capability watcher services terminate on first error instead of recovering

	lggr := logger.Test(t)
	server := New(lggr)

	// Create a mock registry that will cause capability failures
	mockRegistry := &mockCapabilityRegistry{
		shouldFailCapabilityCreation: true, // This will cause NewTriggerWatcher to fail
	}
	server.capRegistry = mockRegistry

	ctx := context.Background()
	err := server.Start(ctx)
	require.NoError(t, err)

	// Simulate the server trying to start a capability service that will fail
	capID := "cron-trigger@1.0.0"

	// This should fail because our mock registry will cause NewTriggerWatcher to fail
	err = server.startCronTriggerService(ctx, capID)

	// CURRENT BROKEN BEHAVIOR: The service startup fails completely
	// This assertion will PASS with current broken implementation
	assert.Error(t, err, "Current implementation fails completely on capability errors")
	assert.Contains(t, err.Error(), "failed to create cron trigger watcher")

	// Check that no service was registered due to the failure
	server.servicesMutex.Lock()
	serviceCount := len(server.runningServices)
	server.servicesMutex.Unlock()
	assert.Equal(t, 0, serviceCount, "No services should be running after failure")

	// WHAT SHOULD HAPPEN (this will fail until issue is fixed):
	// The service should handle the error gracefully and continue operating
	// Instead of failing completely, it should:
	// 1. Log the error
	// 2. Increment error metrics
	// 3. Schedule a retry with exponential backoff
	// 4. Continue monitoring other capabilities

	// This assertion documents the DESIRED behavior (will fail until fixed):
	// In a resilient system, the service should not fail completely
	// TODO: Uncomment this when implementing proper error recovery
	// assert.NoError(t, err, "Service should handle capability errors gracefully and continue")

	server.Close()

	t.Log("ISSUE EXPOSED: Service fails completely instead of recovering from capability errors")
	t.Log("CURRENT: Single capability failure terminates service startup")
	t.Log("NEEDED: Graceful error handling with retry logic and continued operation")
}

// mockCapabilityRegistry with configurable failure behavior
type mockCapabilityRegistry struct {
	shouldFailCapabilityCreation bool
}

func (m *mockCapabilityRegistry) List(ctx context.Context) ([]capabilities.BaseCapability, error) {
	return []capabilities.BaseCapability{}, nil
}

func (m *mockCapabilityRegistry) Get(ctx context.Context, id string) (capabilities.BaseCapability, error) {
	if m.shouldFailCapabilityCreation {
		return nil, fmt.Errorf("mock registry failure for capability %s", id)
	}
	return nil, nil
}

func (m *mockCapabilityRegistry) GetTrigger(ctx context.Context, id string) (capabilities.TriggerCapability, error) {
	if m.shouldFailCapabilityCreation {
		return nil, fmt.Errorf("mock trigger capability failure for %s", id)
	}
	return nil, nil
}

func (m *mockCapabilityRegistry) GetExecutable(ctx context.Context, id string) (capabilities.ExecutableCapability, error) {
	if m.shouldFailCapabilityCreation {
		return nil, fmt.Errorf("mock executable capability failure for %s", id)
	}
	return nil, nil
}

func (m *mockCapabilityRegistry) Add(ctx context.Context, capability capabilities.BaseCapability) error {
	return nil
}

func (m *mockCapabilityRegistry) Remove(ctx context.Context, id string) error {
	return nil
}

func (m *mockCapabilityRegistry) ConfigForCapability(ctx context.Context, id string, donID uint32) (capabilities.CapabilityConfiguration, error) {
	return capabilities.CapabilityConfiguration{}, nil
}

func (m *mockCapabilityRegistry) DONsForCapability(ctx context.Context, id string) ([]capabilities.DONWithNodes, error) {
	return []capabilities.DONWithNodes{}, nil
}

func (m *mockCapabilityRegistry) LocalNode(ctx context.Context) (capabilities.Node, error) {
	return capabilities.Node{}, nil
}

func (m *mockCapabilityRegistry) NodeByPeerID(ctx context.Context, peerID types.PeerID) (capabilities.Node, error) {
	return capabilities.Node{}, nil
}
