package action

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	llotypes "github.com/smartcontractkit/chainlink-common/pkg/types/llo"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

// TestLLOTransmitActionImplementsInterface verifies interface implementation
func TestLLOTransmitActionImplementsInterface(t *testing.T) {
	var _ capabilities.ActionCapability = (*LLOTransmitAction)(nil)
}

// TestNewLLOTransmitAction tests action creation
func TestNewLLOTransmitAction(t *testing.T) {
	lggr, err := logger.New()
	require.NoError(t, err)
	
	config := Config{
		DonID:          1,
		VerboseLogging: false,
		FromAccount:    "test-account",
	}
	
	action, err := NewLLOTransmitAction(lggr, config, []SubTransmitter{})
	require.NoError(t, err)
	require.NotNil(t, action)
	
	// Verify capability info
	info, err := action.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "llo-transmit@1.0.0", info.ID)
	assert.Equal(t, capabilities.CapabilityTypeAction, info.CapabilityType)
	assert.Equal(t, "Transmits LLO reports to configured destinations", info.Description)
}

// TestActionLifecycle tests Start and Close
func TestActionLifecycle(t *testing.T) {
	lggr, _ := logger.New()
	
	mockTx := &mockTransmitter{}
	config := Config{
		DonID: 1,
	}
	
	action, err := NewLLOTransmitAction(lggr, config, []SubTransmitter{mockTx})
	require.NoError(t, err)
	
	ctx := context.Background()
	
	// Start
	err = action.Start(ctx)
	assert.NoError(t, err)
	assert.True(t, mockTx.startCalled)
	
	// Close
	err = action.Close()
	assert.NoError(t, err)
	assert.True(t, mockTx.closeCalled)
}

// TestExecuteWithSingleTransmitter tests successful transmission to one transmitter
func TestExecuteWithSingleTransmitter(t *testing.T) {
	lggr, _ := logger.New()
	
	mockTx := &mockTransmitter{shouldFail: false}
	config := Config{
		DonID:       1,
		FromAccount: "test",
	}
	
	action, _ := NewLLOTransmitAction(lggr, config, []SubTransmitter{mockTx})
	
	// Create request
	req := Request{
		ConfigDigest: types.ConfigDigest{1, 2, 3, 4, 5},
		SeqNr:        42,
		Report:       []byte("test-report-data"),
		ReportInfo: llotypes.ReportInfo{
			LifeCycleStage: "production",
			ReportFormat:   "llo",
		},
		Signatures: []types.AttributedOnchainSignature{
			{Signature: []byte("sig1"), Signer: 1},
		},
	}
	
	value, _ := capabilities.WrapAny(req)
	capReq := capabilities.CapabilityRequest{
		Inputs: value,
		Metadata: capabilities.RequestMetadata{
			WorkflowExecutionID: "test-workflow-123",
		},
	}
	
	// Execute
	resp, err := action.Execute(context.Background(), capReq)
	require.NoError(t, err)
	
	// Parse response
	var result Response
	err = resp.Value.UnwrapTo(&result)
	require.NoError(t, err)
	
	// Verify success
	assert.True(t, result.Success, "Expected successful transmission")
	assert.Equal(t, uint32(1), result.SuccessfulTransmissions)
	assert.Equal(t, uint32(0), result.FailedTransmissions)
	assert.Empty(t, result.Error)
	assert.Len(t, result.TransmitterResults, 1)
	assert.True(t, result.TransmitterResults[0].Success)
	
	// Verify transmitter received the report
	assert.True(t, mockTx.transmitted)
	assert.Equal(t, uint64(42), mockTx.seqNr)
}

// TestExecuteWithMultipleTransmitters tests fan-out to multiple transmitters
func TestExecuteWithMultipleTransmitters(t *testing.T) {
	lggr, _ := logger.New()
	
	mockTx1 := &mockTransmitter{shouldFail: false}
	mockTx2 := &mockTransmitter{shouldFail: false}
	mockTx3 := &mockTransmitter{shouldFail: false}
	
	config := Config{DonID: 1}
	action, _ := NewLLOTransmitAction(lggr, config, []SubTransmitter{mockTx1, mockTx2, mockTx3})
	
	req := Request{
		ConfigDigest: types.ConfigDigest{1, 2, 3},
		SeqNr:        100,
		Report:       []byte("multi-transmit-test"),
		ReportInfo: llotypes.ReportInfo{
			LifeCycleStage: "production",
			ReportFormat:   "llo",
		},
	}
	
	value, _ := capabilities.WrapAny(req)
	capReq := capabilities.CapabilityRequest{
		Inputs: value,
		Metadata: capabilities.RequestMetadata{
			WorkflowExecutionID: "multi-test",
		},
	}
	
	resp, err := action.Execute(context.Background(), capReq)
	require.NoError(t, err)
	
	var result Response
	err = resp.Value.UnwrapTo(&result)
	require.NoError(t, err)
	
	// All three should succeed
	assert.True(t, result.Success)
	assert.Equal(t, uint32(3), result.SuccessfulTransmissions)
	assert.Equal(t, uint32(0), result.FailedTransmissions)
	assert.Len(t, result.TransmitterResults, 3)
	
	// All transmitters should have received the report
	assert.True(t, mockTx1.transmitted)
	assert.True(t, mockTx2.transmitted)
	assert.True(t, mockTx3.transmitted)
}

// TestExecuteWithPartialFailure tests handling of partial transmission failures
func TestExecuteWithPartialFailure(t *testing.T) {
	lggr, _ := logger.New()
	
	mockTx1 := &mockTransmitter{shouldFail: false}
	mockTx2 := &mockTransmitter{shouldFail: true} // This one will fail
	mockTx3 := &mockTransmitter{shouldFail: false}
	
	config := Config{DonID: 1}
	action, _ := NewLLOTransmitAction(lggr, config, []SubTransmitter{mockTx1, mockTx2, mockTx3})
	
	req := Request{
		ConfigDigest: types.ConfigDigest{1, 2, 3},
		SeqNr:        200,
		Report:       []byte("partial-failure-test"),
		ReportInfo: llotypes.ReportInfo{
			LifeCycleStage: "production",
			ReportFormat:   "llo",
		},
	}
	
	value, _ := capabilities.WrapAny(req)
	capReq := capabilities.CapabilityRequest{
		Inputs: value,
		Metadata: capabilities.RequestMetadata{
			WorkflowExecutionID: "partial-fail-test",
		},
	}
	
	resp, err := action.Execute(context.Background(), capReq)
	require.NoError(t, err)
	
	var result Response
	err = resp.Value.UnwrapTo(&result)
	require.NoError(t, err)
	
	// Overall should fail but some transmissions succeeded
	assert.False(t, result.Success, "Expected overall failure")
	assert.Equal(t, uint32(2), result.SuccessfulTransmissions)
	assert.Equal(t, uint32(1), result.FailedTransmissions)
	assert.NotEmpty(t, result.Error, "Should have error message")
	assert.Len(t, result.TransmitterResults, 3)
	
	// Check individual results
	successCount := 0
	failCount := 0
	for _, txResult := range result.TransmitterResults {
		if txResult.Success {
			successCount++
		} else {
			failCount++
			assert.Contains(t, txResult.Error, "mock transmission failure")
		}
	}
	assert.Equal(t, 2, successCount)
	assert.Equal(t, 1, failCount)
}

// TestExecuteWithAllFailures tests when all transmitters fail
func TestExecuteWithAllFailures(t *testing.T) {
	lggr, _ := logger.New()
	
	mockTx1 := &mockTransmitter{shouldFail: true}
	mockTx2 := &mockTransmitter{shouldFail: true}
	
	config := Config{DonID: 1}
	action, _ := NewLLOTransmitAction(lggr, config, []SubTransmitter{mockTx1, mockTx2})
	
	req := Request{
		ConfigDigest: types.ConfigDigest{1, 2, 3},
		SeqNr:        300,
		Report:       []byte("all-fail-test"),
		ReportInfo: llotypes.ReportInfo{
			LifeCycleStage: "production",
			ReportFormat:   "llo",
		},
	}
	
	value, _ := capabilities.WrapAny(req)
	capReq := capabilities.CapabilityRequest{
		Inputs: value,
		Metadata: capabilities.RequestMetadata{
			WorkflowExecutionID: "all-fail-test",
		},
	}
	
	resp, err := action.Execute(context.Background(), capReq)
	require.NoError(t, err)
	
	var result Response
	err = resp.Value.UnwrapTo(&result)
	require.NoError(t, err)
	
	// All should fail
	assert.False(t, result.Success)
	assert.Equal(t, uint32(0), result.SuccessfulTransmissions)
	assert.Equal(t, uint32(2), result.FailedTransmissions)
	assert.NotEmpty(t, result.Error)
}

// TestHealthReport tests health reporting
func TestHealthReport(t *testing.T) {
	lggr, _ := logger.New()
	
	mockTx1 := &mockTransmitter{healthy: true}
	mockTx2 := &mockTransmitter{healthy: false}
	
	config := Config{DonID: 1}
	action, _ := NewLLOTransmitAction(lggr, config, []SubTransmitter{mockTx1, mockTx2})
	
	ctx := context.Background()
	err := action.Start(ctx)
	require.NoError(t, err)
	
	health := action.HealthReport()
	
	// Should have entries for main action and sub-transmitters
	assert.Contains(t, health, "LLOTransmitAction")
	assert.NoError(t, health["LLOTransmitAction"])
	
	// Should have sub-transmitter health
	hasUnhealthy := false
	for name, err := range health {
		if name != "LLOTransmitAction" && err != nil {
			hasUnhealthy = true
		}
	}
	assert.True(t, hasUnhealthy, "Should have unhealthy sub-transmitter")
}

// TestRegisterToWorkflow tests workflow registration
func TestRegisterToWorkflow(t *testing.T) {
	lggr, _ := logger.New()
	config := Config{DonID: 1}
	action, _ := NewLLOTransmitAction(lggr, config, []SubTransmitter{})
	
	ctx := context.Background()
	req := capabilities.RegisterToWorkflowRequest{
		Metadata: capabilities.RegistrationMetadata{
			WorkflowID: "test-workflow",
		},
	}
	
	err := action.RegisterToWorkflow(ctx, req)
	assert.NoError(t, err, "RegisterToWorkflow should not error")
}

// TestUnregisterFromWorkflow tests workflow unregistration
func TestUnregisterFromWorkflow(t *testing.T) {
	lggr, _ := logger.New()
	config := Config{DonID: 1}
	action, _ := NewLLOTransmitAction(lggr, config, []SubTransmitter{})
	
	ctx := context.Background()
	req := capabilities.UnregisterFromWorkflowRequest{
		Metadata: capabilities.RegistrationMetadata{
			WorkflowID: "test-workflow",
		},
	}
	
	err := action.UnregisterFromWorkflow(ctx, req)
	assert.NoError(t, err, "UnregisterFromWorkflow should not error")
}

// TestVerboseLogging tests that verbose logging doesn't break functionality
func TestVerboseLogging(t *testing.T) {
	lggr, _ := logger.New()
	
	mockTx := &mockTransmitter{}
	config := Config{
		DonID:          1,
		VerboseLogging: true, // Enable verbose logging
	}
	
	action, _ := NewLLOTransmitAction(lggr, config, []SubTransmitter{mockTx})
	
	req := Request{
		ConfigDigest: types.ConfigDigest{1, 2, 3},
		SeqNr:        999,
		Report:       []byte("verbose-test"),
		ReportInfo: llotypes.ReportInfo{
			LifeCycleStage: "production",
			ReportFormat:   "llo",
		},
	}
	
	value, _ := capabilities.WrapAny(req)
	capReq := capabilities.CapabilityRequest{
		Inputs: value,
		Metadata: capabilities.RequestMetadata{
			WorkflowExecutionID: "verbose-test",
		},
	}
	
	resp, err := action.Execute(context.Background(), capReq)
	require.NoError(t, err)
	
	var result Response
	err = resp.Value.UnwrapTo(&result)
	require.NoError(t, err)
	assert.True(t, result.Success)
}

// TestConcurrentExecutions tests that the action handles concurrent requests safely
func TestConcurrentExecutions(t *testing.T) {
	lggr, _ := logger.New()
	
	mockTx := &mockTransmitter{}
	config := Config{DonID: 1}
	action, _ := NewLLOTransmitAction(lggr, config, []SubTransmitter{mockTx})
	
	ctx := context.Background()
	err := action.Start(ctx)
	require.NoError(t, err)
	defer action.Close()
	
	// Run multiple concurrent executions
	numConcurrent := 10
	errors := make(chan error, numConcurrent)
	
	for i := 0; i < numConcurrent; i++ {
		go func(seqNr uint64) {
			req := Request{
				ConfigDigest: types.ConfigDigest{1, 2, 3},
				SeqNr:        seqNr,
				Report:       []byte(fmt.Sprintf("concurrent-test-%d", seqNr)),
				ReportInfo: llotypes.ReportInfo{
					LifeCycleStage: "production",
					ReportFormat:   "llo",
				},
			}
			
			value, _ := capabilities.WrapAny(req)
			capReq := capabilities.CapabilityRequest{
				Inputs: value,
				Metadata: capabilities.RequestMetadata{
					WorkflowExecutionID: fmt.Sprintf("concurrent-%d", seqNr),
				},
			}
			
			_, err := action.Execute(context.Background(), capReq)
			errors <- err
		}(uint64(i))
	}
	
	// Collect errors
	for i := 0; i < numConcurrent; i++ {
		err := <-errors
		assert.NoError(t, err, "Concurrent execution failed")
	}
}

// BenchmarkExecute benchmarks the Execute method
func BenchmarkExecute(b *testing.B) {
	lggr, _ := logger.New()
	mockTx := &mockTransmitter{}
	config := Config{DonID: 1}
	action, _ := NewLLOTransmitAction(lggr, config, []SubTransmitter{mockTx})
	
	req := Request{
		ConfigDigest: types.ConfigDigest{1, 2, 3},
		SeqNr:        1,
		Report:       []byte("benchmark-test"),
		ReportInfo: llotypes.ReportInfo{
			LifeCycleStage: "production",
			ReportFormat:   "llo",
		},
	}
	
	value, _ := capabilities.WrapAny(req)
	capReq := capabilities.CapabilityRequest{
		Inputs: value,
		Metadata: capabilities.RequestMetadata{
			WorkflowExecutionID: "benchmark",
		},
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = action.Execute(context.Background(), capReq)
	}
}

// mockTransmitter implements SubTransmitter for testing
type mockTransmitter struct {
	shouldFail  bool
	transmitted bool
	seqNr       uint64
	report      []byte
	startCalled bool
	closeCalled bool
	healthy     bool
}

func (m *mockTransmitter) Transmit(ctx context.Context, cd types.ConfigDigest, seqNr uint64, report interface{}, sigs interface{}) error {
	m.transmitted = true
	m.seqNr = seqNr
	
	// Simulate some work
	time.Sleep(1 * time.Millisecond)
	
	if m.shouldFail {
		return errors.New("mock transmission failure")
	}
	return nil
}

func (m *mockTransmitter) FromAccount(ctx context.Context) (types.Account, error) {
	return types.Account("mock-account"), nil
}

func (m *mockTransmitter) Start(ctx context.Context) error {
	m.startCalled = true
	return nil
}

func (m *mockTransmitter) Close() error {
	m.closeCalled = true
	return nil
}

func (m *mockTransmitter) HealthReport() map[string]error {
	if m.healthy {
		return map[string]error{"MockTransmitter": nil}
	}
	return map[string]error{"MockTransmitter": errors.New("unhealthy")}
}

func (m *mockTransmitter) Name() string {
	return "MockTransmitter"
}

func (m *mockTransmitter) Ready() error {
	return nil
}

