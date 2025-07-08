package trigger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

func TestWorkflow_Trigger_Success(t *testing.T) {
	t.Parallel()

	workflowID := "test-workflow-123"
	authorizedKeys := map[string]gateway.AuthorizedKey{
		"key1": {KeyType: "ECDSA", PublicKey: "0x123"},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	wf := newWorkflow(workflowID, authorizedKeys, sendCh)

	trigger := capabilities.TriggerAndId[*http.Payload]{
		Id: "trigger-123",
		Trigger: &http.Payload{
			Input: nil,
		},
	}

	err := wf.trigger(t.Context(), trigger)
	require.NoError(t, err)

	// Verify the trigger was sent
	select {
	case receivedTrigger := <-sendCh:
		require.Equal(t, trigger, receivedTrigger)
	case <-t.Context().Done():
		t.Fatal("Expected trigger to be sent but channel was empty")
	}
}

func TestWorkflow_Trigger_ContextCanceled(t *testing.T) {
	t.Parallel()

	workflowID := "test-workflow-123"
	authorizedKeys := map[string]gateway.AuthorizedKey{
		"key1": {KeyType: "ECDSA", PublicKey: "0x123"},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	wf := newWorkflow(workflowID, authorizedKeys, sendCh)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	trigger := capabilities.TriggerAndId[*http.Payload]{
		Id: "trigger-123",
		Trigger: &http.Payload{
			Input: nil,
		},
	}

	err := wf.trigger(ctx, trigger)
	require.Error(t, err)
	require.Equal(t, errContextCanceled, err)
}

func TestWorkflow_Trigger_ChannelFull(t *testing.T) {
	t.Parallel()

	workflowID := "test-workflow-123"
	authorizedKeys := map[string]gateway.AuthorizedKey{
		"key1": {KeyType: "ECDSA", PublicKey: "0x123"},
	}
	// Create a channel with no buffer
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload])

	wf := newWorkflow(workflowID, authorizedKeys, sendCh)

	ctx := context.Background()
	trigger := capabilities.TriggerAndId[*http.Payload]{
		Id: "trigger-123",
		Trigger: &http.Payload{
			Input: nil,
		},
	}

	err := wf.trigger(ctx, trigger)
	require.Error(t, err)
	require.Equal(t, errFullChannel, err)
}

func TestWorkflow_Trigger_WorkflowClosed(t *testing.T) {
	t.Parallel()

	workflowID := "test-workflow-123"
	authorizedKeys := map[string]gateway.AuthorizedKey{
		"key1": {KeyType: "ECDSA", PublicKey: "0x123"},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	wf := newWorkflow(workflowID, authorizedKeys, sendCh)
	wf.close() // Close the workflow

	ctx := context.Background()
	trigger := capabilities.TriggerAndId[*http.Payload]{
		Id: "trigger-123",
		Trigger: &http.Payload{
			Input: nil,
		},
	}

	err := wf.trigger(ctx, trigger)
	require.Error(t, err)
	require.Equal(t, errWorkflowClosed, err)
}

func TestWorkflow_Close(t *testing.T) {
	t.Parallel()

	workflowID := "test-workflow-123"
	authorizedKeys := map[string]gateway.AuthorizedKey{
		"key1": {KeyType: "ECDSA", PublicKey: "0x123"},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	wf := newWorkflow(workflowID, authorizedKeys, sendCh)
	require.False(t, wf.closed)

	wf.close()
	require.True(t, wf.closed)

	// Test that calling close again doesn't panic
	wf.close()
	require.True(t, wf.closed)
}

func TestWorkflowStore_RegisterWorkflow(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := NewWorkflowStore(lggr)

	workflowID := "test-workflow-123"
	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
		{KeyType: "ECDSA", PublicKey: "0x456"},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	err := store.RegisterWorkflow(workflowID, authorizedKeys, sendCh)
	require.NoError(t, err)

	wf, err := store.GetWorkflow(workflowID)
	require.NoError(t, err)
	require.NotNil(t, wf)
	require.Equal(t, workflowID, wf.workflowID)
	require.Equal(t, 2, len(wf.authorizedKeys))
	require.Contains(t, wf.authorizedKeys, "0x123")
	require.Contains(t, wf.authorizedKeys, "0x456")
}

func TestWorkflowStore_RegisterWorkflow_Duplicate(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := NewWorkflowStore(lggr)

	workflowID := "test-workflow-123"
	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
	}
	sendCh1 := make(chan<- capabilities.TriggerAndId[*http.Payload], 1)
	sendCh2 := make(chan<- capabilities.TriggerAndId[*http.Payload], 1)

	// Register first workflow
	err := store.RegisterWorkflow(workflowID, authorizedKeys, sendCh1)
	require.NoError(t, err)

	// Register duplicate workflow (should replace the first one)
	err = store.RegisterWorkflow(workflowID, authorizedKeys, sendCh2)
	require.NoError(t, err)

	// Verify the workflow was replaced
	wf, err := store.GetWorkflow(workflowID)
	require.NoError(t, err)
	require.NotNil(t, wf)
	require.Equal(t, sendCh2, wf.sendCh)
}

func TestWorkflowStore_UnregisterWorkflow_Success(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := NewWorkflowStore(lggr)

	workflowID := "test-workflow-123"
	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	err := store.RegisterWorkflow(workflowID, authorizedKeys, sendCh)
	require.NoError(t, err)

	wf, err := store.GetWorkflow(workflowID)
	require.NoError(t, err)
	require.NotNil(t, wf)
	require.Equal(t, workflowID, wf.workflowID)

	err = store.UnregisterWorkflow(workflowID)
	require.NoError(t, err)

	_, err = store.GetWorkflow(workflowID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestWorkflowStore_UnregisterWorkflow_NotFound(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := NewWorkflowStore(lggr)

	workflowID := "non-existent-workflow"
	err := store.UnregisterWorkflow(workflowID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	wf, err := store.GetWorkflow(workflowID)
	require.Error(t, err)
	require.Nil(t, wf)
	require.Contains(t, err.Error(), "not found")
}

func TestWorkflowStore_GetWorkflows_Empty(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := NewWorkflowStore(lggr)

	workflows, err := store.GetWorkflows()
	require.NoError(t, err)
	require.NotNil(t, workflows)
	require.Equal(t, 0, len(workflows))
}

func TestWorkflowStore_GetWorkflows_Multiple(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := NewWorkflowStore(lggr)

	// Register multiple workflows
	workflowID1 := "test-workflow-1"
	workflowID2 := "test-workflow-2"
	workflowID3 := "test-workflow-3"

	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
	}

	sendCh1 := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	sendCh2 := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	sendCh3 := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	err := store.RegisterWorkflow(workflowID1, authorizedKeys, sendCh1)
	require.NoError(t, err)
	err = store.RegisterWorkflow(workflowID2, authorizedKeys, sendCh2)
	require.NoError(t, err)
	err = store.RegisterWorkflow(workflowID3, authorizedKeys, sendCh3)
	require.NoError(t, err)

	// Get all workflows
	workflows, err := store.GetWorkflows()
	require.NoError(t, err)
	require.Equal(t, 3, len(workflows))

	// Verify all workflow IDs are present
	workflowIDs := make(map[string]bool)
	for _, wf := range workflows {
		workflowIDs[wf.workflowID] = true
	}

	require.True(t, workflowIDs[workflowID1])
	require.True(t, workflowIDs[workflowID2])
	require.True(t, workflowIDs[workflowID3])
}
