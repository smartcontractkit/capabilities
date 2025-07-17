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
	wf, sendCh := testWorkflow()
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

	wf, _ := testWorkflow()
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

	wf, _ := testWorkflow()
	ctx := context.Background()
	trigger := capabilities.TriggerAndId[*http.Payload]{
		Id: "trigger-123",
		Trigger: &http.Payload{
			Input: nil,
		},
	}

	err := wf.trigger(ctx, trigger)
	require.NoError(t, err)
	err = wf.trigger(ctx, trigger)
	require.Error(t, err)
	require.Equal(t, errFullChannel, err)
}

func TestWorkflow_Trigger_WorkflowClosed(t *testing.T) {
	t.Parallel()

	wf, _ := testWorkflow()
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
	wf, _ := testWorkflow()
	require.False(t, wf.closed)

	wf.close()
	require.True(t, wf.closed)

	// Test that calling close again doesn't panic
	wf.close()
	require.True(t, wf.closed)
}

func TestWorkflowStore_upsertWorkflow(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)
	wf, _ := testWorkflow()
	store.upsertWorkflow(wf)

	w, exists := store.getWorkflowByID(wf.workflowSelector.WorkflowID)
	require.True(t, exists)
	require.NotNil(t, w)
	require.Equal(t, wf.workflowSelector, w.workflowSelector)
	require.Equal(t, 2, len(w.authorizedKeys))
	for key := range wf.authorizedKeys {
		require.Contains(t, w.authorizedKeys, key)
	}
}

func TestWorkflowStore_upsertWorkflow_Duplicate(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)
	w1, _ := testWorkflow()
	w2, _ := testWorkflow()

	// Add first workflow
	store.upsertWorkflow(w1)

	// Add second workflow with same ID (this should replace the first)
	store.upsertWorkflow(w2)

	// Verify the workflow was replaced - since both have same ID/reference,
	// the second one should be present
	wf, exist := store.getWorkflowByID(w1.workflowSelector.WorkflowID)
	require.True(t, exist)
	require.NotNil(t, wf)
	require.Equal(t, w2.sendCh, wf.sendCh)
}

func TestWorkflowStore_removeWorkflow_Success(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)
	w, _ := testWorkflow()
	store.upsertWorkflow(w)

	wf, exists := store.getWorkflowByID(w.workflowSelector.WorkflowID)
	require.True(t, exists)
	require.Equal(t, w.workflowSelector, wf.workflowSelector)

	err := store.removeWorkflow(w.workflowSelector.WorkflowID)
	require.NoError(t, err)

	_, exists = store.getWorkflowByID(w.workflowSelector.WorkflowID)
	require.False(t, exists)
}

func TestWorkflowStore_removeWorkflow_NotFound(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	workflowID := "non-existent-workflow"
	err := store.removeWorkflow(workflowID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	wf, exists := store.getWorkflowByID(workflowID)
	require.False(t, exists)
	require.Nil(t, wf)
}

func TestWorkflowStore_GetWorkflows_Empty(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	workflows := store.getWorkflows()
	require.NotNil(t, workflows)
	require.Equal(t, 0, len(workflows))
}

func TestWorkflowStore_GetWorkflows_Multiple(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
	}

	sendCh1 := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	sendCh2 := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	sendCh3 := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	wfSelector1 := gateway.WorkflowSelector{
		WorkflowID:    "workflow-id-1",
		WorkflowOwner: "owner1",
		WorkflowName:  "name1",
		WorkflowTag:   "tag1",
	}
	wfSelector2 := gateway.WorkflowSelector{
		WorkflowID:    "workflow-id-2",
		WorkflowOwner: "owner2",
		WorkflowName:  "name2",
		WorkflowTag:   "tag2",
	}
	wfSelector3 := gateway.WorkflowSelector{
		WorkflowID:    "workflow-id-3",
		WorkflowOwner: "owner3",
		WorkflowName:  "name3",
		WorkflowTag:   "tag3",
	}

	wf1 := newWorkflow(wfSelector1, authorizedKeys, sendCh1)
	wf2 := newWorkflow(wfSelector2, authorizedKeys, sendCh2)
	wf3 := newWorkflow(wfSelector3, authorizedKeys, sendCh3)

	store.upsertWorkflow(wf1)
	store.upsertWorkflow(wf2)
	store.upsertWorkflow(wf3)

	// Get all workflows
	workflows := store.getWorkflows()
	require.Equal(t, 3, len(workflows))

	// Verify all workflow IDs are present
	workflowIDs := make(map[string]bool)
	for _, wf := range workflows {
		workflowIDs[wf.workflowSelector.WorkflowID] = true
	}

	require.True(t, workflowIDs[wfSelector1.WorkflowID])
	require.True(t, workflowIDs[wfSelector2.WorkflowID])
	require.True(t, workflowIDs[wfSelector3.WorkflowID])
}

func TestWorkflowStore_getWorkflowIDByReference_Success(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)
	wf, _ := testWorkflow()
	store.upsertWorkflow(wf)

	workflowID, exists := store.getWorkflowIDByReference(
		wf.workflowSelector.WorkflowOwner,
		wf.workflowSelector.WorkflowName,
		wf.workflowSelector.WorkflowTag,
	)
	require.True(t, exists)
	require.Equal(t, wf.workflowSelector.WorkflowID, workflowID)
}

func TestWorkflowStore_getWorkflowIDByReference_NotFound(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	workflowID, exists := store.getWorkflowIDByReference(
		"non-existent-owner",
		"non-existent-name",
		"non-existent-tag",
	)
	require.False(t, exists)
	require.Empty(t, workflowID)
}

func TestWorkflowStore_getWorkflowIDByReference_EmptyStore(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	workflowID, exists := store.getWorkflowIDByReference(
		"owner",
		"name",
		"tag",
	)
	require.False(t, exists)
	require.Empty(t, workflowID)
}

func TestWorkflowStore_upsertWorkflow_ReplaceWithSameReference(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	// Create two workflows with same reference but different IDs
	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
	}
	sendCh1 := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	sendCh2 := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	selector1 := gateway.WorkflowSelector{
		WorkflowID:    "workflow-id-1",
		WorkflowOwner: "same-owner",
		WorkflowName:  "same-name",
		WorkflowTag:   "same-tag",
	}
	selector2 := gateway.WorkflowSelector{
		WorkflowID:    "workflow-id-2",
		WorkflowOwner: "same-owner",
		WorkflowName:  "same-name",
		WorkflowTag:   "same-tag",
	}

	wf1 := newWorkflow(selector1, authorizedKeys, sendCh1)
	wf2 := newWorkflow(selector2, authorizedKeys, sendCh2)

	// Add first workflow
	store.upsertWorkflow(wf1)

	// Verify first workflow is there
	workflow, exists := store.getWorkflowByID("workflow-id-1")
	require.True(t, exists)
	require.Equal(t, "workflow-id-1", workflow.workflowSelector.WorkflowID)

	// Reference should point to first workflow
	workflowID, exists := store.getWorkflowIDByReference("same-owner", "same-name", "same-tag")
	require.True(t, exists)
	require.Equal(t, "workflow-id-1", workflowID)

	// Add second workflow with same reference
	store.upsertWorkflow(wf2)

	// First workflow should be removed
	_, exists = store.getWorkflowByID("workflow-id-1")
	require.False(t, exists)

	// Second workflow should exist
	workflow, exists = store.getWorkflowByID("workflow-id-2")
	require.True(t, exists)
	require.Equal(t, "workflow-id-2", workflow.workflowSelector.WorkflowID)

	// Reference should now point to second workflow
	workflowID, exists = store.getWorkflowIDByReference("same-owner", "same-name", "same-tag")
	require.True(t, exists)
	require.Equal(t, "workflow-id-2", workflowID)
}

func TestWorkflowStore_removeWorkflow_RemovesReference(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)
	wf, _ := testWorkflow()
	store.upsertWorkflow(wf)

	// Verify workflow and reference exist
	_, exists := store.getWorkflowByID(wf.workflowSelector.WorkflowID)
	require.True(t, exists)

	workflowID, exists := store.getWorkflowIDByReference(
		wf.workflowSelector.WorkflowOwner,
		wf.workflowSelector.WorkflowName,
		wf.workflowSelector.WorkflowTag,
	)
	require.True(t, exists)
	require.Equal(t, wf.workflowSelector.WorkflowID, workflowID)

	// Remove workflow
	err := store.removeWorkflow(wf.workflowSelector.WorkflowID)
	require.NoError(t, err)

	// Verify both workflow and reference are removed
	_, exists = store.getWorkflowByID(wf.workflowSelector.WorkflowID)
	require.False(t, exists)

	_, exists = store.getWorkflowIDByReference(
		wf.workflowSelector.WorkflowOwner,
		wf.workflowSelector.WorkflowName,
		wf.workflowSelector.WorkflowTag,
	)
	require.False(t, exists)
}

func TestWorkflow_AuthorizedKeys_Conversion(t *testing.T) {
	t.Parallel()

	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
		{KeyType: "ECDSA", PublicKey: "0x456"},
		{KeyType: "RSA", PublicKey: "0x789"},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	workflowSelector := workflowSelector("test-workflow")

	wf := newWorkflow(workflowSelector, authorizedKeys, sendCh)

	// Verify all keys are properly converted to map
	require.Equal(t, 3, len(wf.authorizedKeys))
	for _, key := range authorizedKeys {
		_, exists := wf.authorizedKeys[key]
		require.True(t, exists, "Key %v should exist in authorized keys map", key)
	}
}

func TestWorkflow_AuthorizedKeys_Empty(t *testing.T) {
	t.Parallel()

	var authorizedKeys []gateway.AuthorizedKey
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	workflowSelector := workflowSelector("test-workflow")

	wf := newWorkflow(workflowSelector, authorizedKeys, sendCh)

	require.Equal(t, 0, len(wf.authorizedKeys))
}

func TestWorkflow_AuthorizedKeys_Duplicates(t *testing.T) {
	t.Parallel()

	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
		{KeyType: "ECDSA", PublicKey: "0x123"}, // Duplicate
		{KeyType: "ECDSA", PublicKey: "0x456"},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	workflowSelector := workflowSelector("test-workflow")

	wf := newWorkflow(workflowSelector, authorizedKeys, sendCh)

	// Map should only contain unique keys
	require.Equal(t, 2, len(wf.authorizedKeys))

	expectedKey1 := gateway.AuthorizedKey{KeyType: "ECDSA", PublicKey: "0x123"}
	expectedKey2 := gateway.AuthorizedKey{KeyType: "ECDSA", PublicKey: "0x456"}

	_, exists1 := wf.authorizedKeys[expectedKey1]
	_, exists2 := wf.authorizedKeys[expectedKey2]

	require.True(t, exists1)
	require.True(t, exists2)
}

func TestWorkflow_Trigger_MultipleSuccessful(t *testing.T) {
	t.Parallel()

	// Create workflow with larger buffer
	workflowID := "test-workflow-multi"
	workflowSelector := workflowSelector(workflowID)
	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 3) // Buffer of 3
	wf := newWorkflow(workflowSelector, authorizedKeys, sendCh)

	ctx := context.Background()

	trigger1 := capabilities.TriggerAndId[*http.Payload]{
		Id:      "trigger-1",
		Trigger: &http.Payload{Input: nil},
	}
	trigger2 := capabilities.TriggerAndId[*http.Payload]{
		Id:      "trigger-2",
		Trigger: &http.Payload{Input: nil},
	}
	trigger3 := capabilities.TriggerAndId[*http.Payload]{
		Id:      "trigger-3",
		Trigger: &http.Payload{Input: nil},
	}

	// Send multiple triggers
	err1 := wf.trigger(ctx, trigger1)
	err2 := wf.trigger(ctx, trigger2)
	err3 := wf.trigger(ctx, trigger3)

	require.NoError(t, err1)
	require.NoError(t, err2)
	require.NoError(t, err3)

	// Verify all triggers were received
	receivedTrigger1 := <-sendCh
	receivedTrigger2 := <-sendCh
	receivedTrigger3 := <-sendCh

	require.Equal(t, trigger1, receivedTrigger1)
	require.Equal(t, trigger2, receivedTrigger2)
	require.Equal(t, trigger3, receivedTrigger3)
}

func TestWorkflow_Trigger_EmptyTriggerId(t *testing.T) {
	t.Parallel()

	wf, _ := testWorkflow()
	ctx := context.Background()

	trigger := capabilities.TriggerAndId[*http.Payload]{
		Id: "", // Empty ID
		Trigger: &http.Payload{
			Input: nil,
		},
	}

	err := wf.trigger(ctx, trigger)
	require.Error(t, err)
}

func TestWorkflowStore_getWorkflowIDByReference_PartialMatch(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)
	wf, _ := testWorkflow()
	store.upsertWorkflow(wf)

	// Test with wrong owner
	workflowID, exists := store.getWorkflowIDByReference(
		"wrong-owner",
		wf.workflowSelector.WorkflowName,
		wf.workflowSelector.WorkflowTag,
	)
	require.False(t, exists)
	require.Empty(t, workflowID)

	// Test with wrong name
	workflowID, exists = store.getWorkflowIDByReference(
		wf.workflowSelector.WorkflowOwner,
		"wrong-name",
		wf.workflowSelector.WorkflowTag,
	)
	require.False(t, exists)
	require.Empty(t, workflowID)

	// Test with wrong tag
	workflowID, exists = store.getWorkflowIDByReference(
		wf.workflowSelector.WorkflowOwner,
		wf.workflowSelector.WorkflowName,
		"wrong-tag",
	)
	require.False(t, exists)
	require.Empty(t, workflowID)
}

func TestWorkflowStore_getWorkflowByID_NotFound(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	// Test with non-existent ID
	workflow, exists := store.getWorkflowByID("non-existent-id")
	require.False(t, exists)
	require.Nil(t, workflow)
}

func TestWorkflowStore_getWorkflowByID_EmptyID(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	// Test with empty ID
	workflow, exists := store.getWorkflowByID("")
	require.False(t, exists)
	require.Nil(t, workflow)
}

func TestWorkflow_Close_MultipleTimes(t *testing.T) {
	t.Parallel()

	wf, sendCh := testWorkflow()
	require.False(t, wf.closed)

	// First close
	wf.close()
	require.True(t, wf.closed)

	// Verify channel is closed
	select {
	case _, ok := <-sendCh:
		require.False(t, ok, "Channel should be closed")
	default:
		t.Fatal("Channel should be closed and readable")
	}

	// Multiple closes should not panic
	wf.close()
	wf.close()
	require.True(t, wf.closed)
}

func TestWorkflow_Trigger_AfterChannelDrain(t *testing.T) {
	t.Parallel()

	// Create workflow with buffered channel
	workflowID := "test-workflow-drain"
	workflowSelector := workflowSelector(workflowID)
	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	wf := newWorkflow(workflowSelector, authorizedKeys, sendCh)

	ctx := context.Background()
	trigger := capabilities.TriggerAndId[*http.Payload]{
		Id:      "trigger-1",
		Trigger: &http.Payload{Input: nil},
	}

	// Fill the channel
	err := wf.trigger(ctx, trigger)
	require.NoError(t, err)

	// Try to send another - should fail due to full channel
	err = wf.trigger(ctx, trigger)
	require.Error(t, err)
	require.Equal(t, errFullChannel, err)

	// Drain the channel
	<-sendCh

	// Now sending should work again
	err = wf.trigger(ctx, trigger)
	require.NoError(t, err)
}

func testWorkflow() (*workflow, chan capabilities.TriggerAndId[*http.Payload]) {
	workflowID := "test-workflow-123"
	workflowSelector := workflowSelector(workflowID)
	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
		{KeyType: "ECDSA", PublicKey: "0x456"},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	return newWorkflow(workflowSelector, authorizedKeys, sendCh), sendCh
}

func workflowSelector(workflowID string) gateway.WorkflowSelector {
	return gateway.WorkflowSelector{
		WorkflowID:    workflowID,
		WorkflowOwner: "owner",
		WorkflowName:  "name",
		WorkflowTag:   "tag",
	}
}
