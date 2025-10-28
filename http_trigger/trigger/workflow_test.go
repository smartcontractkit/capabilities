package trigger

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
)

// Test constants for valid workflow identifiers
const (
	testWorkflowID    = "0x217ca1cb7b52136b3baedb2a13e4609fa86439b87a1bc48fea6d95f19444cf72"
	testWorkflowOwner = "0x1234567890123456789012345678901234567890"
	testWorkflowTag   = "tag"

	testWorkflowID1 = "0x1111111111111111111111111111111111111111111111111111111111111111"
	testWorkflowID2 = "0x2222222222222222222222222222222222222222222222222222222222222222"
	testWorkflowID3 = "0x3333333333333333333333333333333333333333333333333333333333333333"

	testWorkflowOwner1 = "0x1111111111111111111111111111111111111111"
	testWorkflowOwner2 = "0x2222222222222222222222222222222222222222"
	testWorkflowOwner3 = "0x3333333333333333333333333333333333333333"

	testWorkflowName1 = "0x11111111111111111111"
	testWorkflowName2 = "0x22222222222222222222"
	testWorkflowName3 = "0x33333333333333333333"
)

var testWorkflowName = ensureHexPrefix(hex.EncodeToString([]byte(workflows.HashTruncateName("workflowName"))))

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
	err := store.upsertWorkflow(wf)
	require.NoError(t, err)

	w, exists := store.getWorkflowByID(wf.workflowSelector.WorkflowID)
	require.True(t, exists)
	require.NotNil(t, w)
	require.Equal(t, wf.workflowSelector, w.workflowSelector)
	require.Equal(t, 2, len(w.authorizedKeys))
	for key := range wf.authorizedKeys {
		require.Contains(t, w.authorizedKeys, key)
	}
}

func TestWorkflowStore_upsertWorkflow_ValidationErrors(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	authorizedKeys := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x123"},
	}

	tests := []struct {
		name     string
		selector gateway.WorkflowSelector
		wantErr  string
	}{
		{
			name: "empty workflowID",
			selector: gateway.WorkflowSelector{
				WorkflowID:    "",
				WorkflowOwner: testWorkflowOwner,
				WorkflowName:  testWorkflowName,
				WorkflowTag:   testWorkflowTag,
			},
			wantErr: "workflowID cannot be empty",
		},
		{
			name: "workflowID without 0x prefix",
			selector: gateway.WorkflowSelector{
				WorkflowID:    "217ca1cb7b52136b3baedb2a13e4609fa86439b87a1bc48fea6d95f19444cf72",
				WorkflowOwner: testWorkflowOwner,
				WorkflowName:  testWorkflowName,
				WorkflowTag:   testWorkflowTag,
			},
			wantErr: "workflowID must have 0x prefix",
		},
		{
			name: "workflowID wrong length",
			selector: gateway.WorkflowSelector{
				WorkflowID:    "0x123",
				WorkflowOwner: testWorkflowOwner,
				WorkflowName:  testWorkflowName,
				WorkflowTag:   testWorkflowTag,
			},
			wantErr: "workflowID must be 66 characters",
		},
		{
			name: "empty workflowOwner",
			selector: gateway.WorkflowSelector{
				WorkflowID:    testWorkflowID,
				WorkflowOwner: "",
				WorkflowName:  testWorkflowName,
				WorkflowTag:   testWorkflowTag,
			},
			wantErr: "workflowOwner cannot be empty",
		},
		{
			name: "workflowOwner without 0x prefix",
			selector: gateway.WorkflowSelector{
				WorkflowID:    testWorkflowID,
				WorkflowOwner: "1234567890123456789012345678901234567890",
				WorkflowName:  testWorkflowName,
				WorkflowTag:   testWorkflowTag,
			},
			wantErr: "workflowOwner must have 0x prefix",
		},
		{
			name: "workflowOwner wrong length",
			selector: gateway.WorkflowSelector{
				WorkflowID:    testWorkflowID,
				WorkflowOwner: "0x123",
				WorkflowName:  testWorkflowName,
				WorkflowTag:   testWorkflowTag,
			},
			wantErr: "workflowOwner must be 42 characters",
		},
		{
			name: "empty workflowName",
			selector: gateway.WorkflowSelector{
				WorkflowID:    testWorkflowID,
				WorkflowOwner: testWorkflowOwner,
				WorkflowName:  "",
				WorkflowTag:   testWorkflowTag,
			},
			wantErr: "workflowName cannot be empty",
		},
		{
			name: "workflowName without 0x prefix",
			selector: gateway.WorkflowSelector{
				WorkflowID:    testWorkflowID,
				WorkflowOwner: testWorkflowOwner,
				WorkflowName:  "74657374776f726b666c6f77",
				WorkflowTag:   testWorkflowTag,
			},
			wantErr: "workflowName must have 0x prefix",
		},
		{
			name: "workflowName wrong length",
			selector: gateway.WorkflowSelector{
				WorkflowID:    testWorkflowID,
				WorkflowOwner: testWorkflowOwner,
				WorkflowName:  "0x123",
				WorkflowTag:   testWorkflowTag,
			},
			wantErr: "workflowName must be 22 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := newWorkflow(tt.selector, authorizedKeys, sendCh)
			err := store.upsertWorkflow(wf)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestWorkflowStore_upsertWorkflow_Duplicate(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)
	w1, _ := testWorkflow()
	w2, _ := testWorkflow()

	// Add first workflow
	err := store.upsertWorkflow(w1)
	require.NoError(t, err)

	// Add second workflow with same ID (this should replace the first)
	err = store.upsertWorkflow(w2)
	require.NoError(t, err)

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
	err := store.upsertWorkflow(w)
	require.NoError(t, err)

	wf, exists := store.getWorkflowByID(w.workflowSelector.WorkflowID)
	require.True(t, exists)
	require.Equal(t, w.workflowSelector, wf.workflowSelector)

	err = store.removeWorkflow(w.workflowSelector.WorkflowID)
	require.NoError(t, err)

	_, exists = store.getWorkflowByID(w.workflowSelector.WorkflowID)
	require.False(t, exists)
}

func TestWorkflowStore_removeWorkflow_NotFound(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	// Use a valid format workflowID that doesn't exist
	workflowID := "0x9999999999999999999999999999999999999999999999999999999999999999"
	err := store.removeWorkflow(workflowID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	wf, exists := store.getWorkflowByID(workflowID)
	require.False(t, exists)
	require.Nil(t, wf)
}

func TestWorkflowStore_removeWorkflow_InvalidID(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	// Test with invalid format workflowIDs
	err := store.removeWorkflow("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid workflowID")

	err = store.removeWorkflow("invalid-id")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid workflowID")

	err = store.removeWorkflow("0x123")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid workflowID")
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
		WorkflowID:    testWorkflowID1,
		WorkflowOwner: testWorkflowOwner1,
		WorkflowName:  testWorkflowName1,
		WorkflowTag:   "tag1",
	}
	wfSelector2 := gateway.WorkflowSelector{
		WorkflowID:    testWorkflowID2,
		WorkflowOwner: testWorkflowOwner2,
		WorkflowName:  testWorkflowName2,
		WorkflowTag:   "tag2",
	}
	wfSelector3 := gateway.WorkflowSelector{
		WorkflowID:    testWorkflowID3,
		WorkflowOwner: testWorkflowOwner3,
		WorkflowName:  testWorkflowName3,
		WorkflowTag:   "tag3",
	}

	wf1 := newWorkflow(wfSelector1, authorizedKeys, sendCh1)
	wf2 := newWorkflow(wfSelector2, authorizedKeys, sendCh2)
	wf3 := newWorkflow(wfSelector3, authorizedKeys, sendCh3)

	err := store.upsertWorkflow(wf1)
	require.NoError(t, err)
	err = store.upsertWorkflow(wf2)
	require.NoError(t, err)
	err = store.upsertWorkflow(wf3)
	require.NoError(t, err)

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
	err := store.upsertWorkflow(wf)
	require.NoError(t, err)

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

	// Use valid format but non-existent reference
	workflowID, exists := store.getWorkflowIDByReference(
		"0x9999999999999999999999999999999999999999",
		"0x99999999999999999999",
		"non-existent-tag",
	)
	require.False(t, exists)
	require.Empty(t, workflowID)
}

func TestWorkflowStore_getWorkflowIDByReference_EmptyStore(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	// Use valid format for owner and name
	workflowID, exists := store.getWorkflowIDByReference(
		testWorkflowOwner,
		testWorkflowName,
		testWorkflowTag,
	)
	require.False(t, exists)
	require.Empty(t, workflowID)
}

func TestWorkflowStore_getWorkflowIDByReference_InvalidInputs(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	// Test with invalid workflowOwner
	workflowID, exists := store.getWorkflowIDByReference(
		"invalid-owner",
		testWorkflowName,
		testWorkflowTag,
	)
	require.False(t, exists)
	require.Empty(t, workflowID)

	// Test with invalid workflowName
	workflowID, exists = store.getWorkflowIDByReference(
		testWorkflowOwner,
		"invalid-name",
		testWorkflowTag,
	)
	require.False(t, exists)
	require.Empty(t, workflowID)

	// Test with empty workflowOwner
	workflowID, exists = store.getWorkflowIDByReference(
		"",
		testWorkflowName,
		testWorkflowTag,
	)
	require.False(t, exists)
	require.Empty(t, workflowID)

	// Test with empty workflowName
	workflowID, exists = store.getWorkflowIDByReference(
		testWorkflowOwner,
		"",
		testWorkflowTag,
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
		WorkflowID:    testWorkflowID1,
		WorkflowOwner: testWorkflowOwner,
		WorkflowName:  testWorkflowName1,
		WorkflowTag:   testWorkflowTag,
	}
	selector2 := gateway.WorkflowSelector{
		WorkflowID:    testWorkflowID2,
		WorkflowOwner: testWorkflowOwner,
		WorkflowName:  testWorkflowName1,
		WorkflowTag:   testWorkflowTag,
	}

	wf1 := newWorkflow(selector1, authorizedKeys, sendCh1)
	wf2 := newWorkflow(selector2, authorizedKeys, sendCh2)

	// Add first workflow
	err := store.upsertWorkflow(wf1)
	require.NoError(t, err)

	// Verify first workflow is there
	workflow, exists := store.getWorkflowByID(testWorkflowID1)
	require.True(t, exists)
	require.Equal(t, testWorkflowID1, workflow.workflowSelector.WorkflowID)

	// Reference should point to first workflow
	workflowID, exists := store.getWorkflowIDByReference(testWorkflowOwner, testWorkflowName1, testWorkflowTag)
	require.True(t, exists)
	require.Equal(t, testWorkflowID1, workflowID)

	// Add second workflow with same reference
	err = store.upsertWorkflow(wf2)
	require.NoError(t, err)

	// First workflow should be removed
	_, exists = store.getWorkflowByID(testWorkflowID1)
	require.False(t, exists)

	// Second workflow should exist
	workflow, exists = store.getWorkflowByID(testWorkflowID2)
	require.True(t, exists)
	require.Equal(t, testWorkflowID2, workflow.workflowSelector.WorkflowID)

	// Reference should now point to second workflow
	workflowID, exists = store.getWorkflowIDByReference(testWorkflowOwner, testWorkflowName1, testWorkflowTag)
	require.True(t, exists)
	require.Equal(t, testWorkflowID2, workflowID)
}

func TestWorkflowStore_removeWorkflow_RemovesReference(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)
	wf, _ := testWorkflow()
	err := store.upsertWorkflow(wf)
	require.NoError(t, err)

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
	err = store.removeWorkflow(wf.workflowSelector.WorkflowID)
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
	err := store.upsertWorkflow(wf)
	require.NoError(t, err)

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

	// Test with valid format but non-existent ID
	workflow, exists := store.getWorkflowByID("0x9999999999999999999999999999999999999999999999999999999999999999")
	require.False(t, exists)
	require.Nil(t, workflow)
}

func TestWorkflowStore_getWorkflowByID_InvalidID(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	store := newWorkflowStore(lggr)

	// Test with invalid format IDs - should return false due to validation
	workflow, exists := store.getWorkflowByID("")
	require.False(t, exists)
	require.Nil(t, workflow)

	workflow, exists = store.getWorkflowByID("invalid-id")
	require.False(t, exists)
	require.Nil(t, workflow)

	workflow, exists = store.getWorkflowByID("0x123") // Too short
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

func TestNormalizeHex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		length   int
		expected string
	}{
		{
			name:     "already normalized workflowID",
			input:    "0x217ca1cb7b52136b3baedb2a13e4609fa86439b87a1bc48fea6d95f19444cf72",
			length:   66,
			expected: "0x217ca1cb7b52136b3baedb2a13e4609fa86439b87a1bc48fea6d95f19444cf72",
		},
		{
			name:     "short workflowID needs padding",
			input:    "0x123",
			length:   66,
			expected: "0x0000000000000000000000000000000000000000000000000000000000000123",
		},
		{
			name:     "workflowID without prefix",
			input:    "123",
			length:   66,
			expected: "0x0000000000000000000000000000000000000000000000000000000000000123",
		},
		{
			name:     "already normalized workflowOwner",
			input:    "0x1234567890123456789012345678901234567890",
			length:   42,
			expected: "0x1234567890123456789012345678901234567890",
		},
		{
			name:     "short workflowOwner needs padding",
			input:    "0x123",
			length:   42,
			expected: "0x0000000000000000000000000000000000000123",
		},
		{
			name:     "empty input",
			input:    "",
			length:   66,
			expected: "0x0000000000000000000000000000000000000000000000000000000000000000",
		},
		{
			name:     "workflowName normalization",
			input:    "0x123",
			length:   22,
			expected: "0x00000000000000000123",
		},
		{
			name:     "input too long returns as-is",
			input:    "0x" + strings.Repeat("1", 100),
			length:   66,
			expected: "0x" + strings.Repeat("1", 100),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeHex(tt.input, tt.length)
			require.Equal(t, tt.expected, result)
		})
	}
}

func testWorkflow() (*workflow, chan capabilities.TriggerAndId[*http.Payload]) {
	workflowSelector := workflowSelector(testWorkflowID)
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
		WorkflowOwner: testWorkflowOwner,
		WorkflowName:  testWorkflowName,
		WorkflowTag:   testWorkflowTag,
	}
}
