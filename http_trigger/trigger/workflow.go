package trigger

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

const (
	// Ethereum address length: 0x + 40 hex chars = 42 chars
	expectedWorkflowOwnerLen = 42
	// Workflow ID length: 0x + 64 hex chars = 66 chars (32 bytes)
	expectedWorkflowIDLen = 66
	// Workflow name hash length: 0x + 20 hex chars = 22 chars (10 bytes)
	workflowNameHashLength = 22
)

var (
	errWorkflowClosed  = fmt.Errorf("workflow is closed, cannot send trigger")
	errContextCanceled = fmt.Errorf("context canceled, cannot send trigger")
	errFullChannel     = fmt.Errorf("workflowID channel is full, cannot send trigger")
)

type workflowStore struct {
	mu                    sync.RWMutex
	workflows             map[string]*workflow         // workflowID -> workflow metadata
	workflowReferenceToID map[workflowReference]string // workflowReference -> workflowID
	lggr                  logger.Logger
}

type workflowReference struct {
	workflowOwner string
	workflowName  string
	workflowTag   string
}

func newWorkflowStore(lggr logger.Logger) *workflowStore {
	return &workflowStore{
		workflows:             make(map[string]*workflow),
		workflowReferenceToID: make(map[workflowReference]string),
		lggr:                  logger.Named(lggr, "WorkflowStore"),
	}
}

// upsertWorkflow either adds a new workflow or updates an existing
// workflow reference (owner/name/tag combination) with new workflow instance.
// upsertWorkflow should be invoked in the order of workflow registration, so that
// the latest workflow instance is always used for the given reference.
func (s *workflowStore) upsertWorkflow(w *workflow) error {
	// Validate workflow fields
	if err := validateWorkflowSelector(w.workflowSelector); err != nil {
		return fmt.Errorf("invalid workflow selector: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	workflowID, exists := s.workflowReferenceToID[workflowReference{
		workflowOwner: w.workflowSelector.WorkflowOwner,
		workflowName:  w.workflowSelector.WorkflowName,
		workflowTag:   w.workflowSelector.WorkflowTag,
	}]
	if exists {
		s.lggr.Debugf("Updating existing workflow reference %s/%s/%s. Removing previous workflow %s",
			w.workflowSelector.WorkflowOwner,
			w.workflowSelector.WorkflowName,
			w.workflowSelector.WorkflowTag,
			workflowID)
		delete(s.workflows, workflowID)
	}
	s.workflows[w.workflowSelector.WorkflowID] = w
	s.workflowReferenceToID[workflowReference{
		workflowOwner: w.workflowSelector.WorkflowOwner,
		workflowName:  w.workflowSelector.WorkflowName,
		workflowTag:   w.workflowSelector.WorkflowTag,
	}] = w.workflowSelector.WorkflowID
	return nil
}

// validateWorkflowSelector validates the workflow selector fields
func validateWorkflowSelector(ws gateway.WorkflowSelector) error {
	if err := validateHexPrefixedField("workflowID", ws.WorkflowID, expectedWorkflowIDLen); err != nil {
		return err
	}
	if err := validateHexPrefixedField("workflowOwner", ws.WorkflowOwner, expectedWorkflowOwnerLen); err != nil {
		return err
	}
	return validateHexPrefixedField("workflowName", ws.WorkflowName, workflowNameHashLength)
}

// validateHexPrefixedField validates that a field is non-empty, has 0x prefix, and matches expected length
func validateHexPrefixedField(fieldName, value string, expectedLength int) error {
	if value == "" {
		return fmt.Errorf("%s cannot be empty", fieldName)
	}
	if !strings.HasPrefix(value, "0x") {
		return fmt.Errorf("%s must have 0x prefix, got: %s", fieldName, value)
	}
	if len(value) != expectedLength {
		return fmt.Errorf("%s must be %d characters, got %d: %s",
			fieldName, expectedLength, len(value), value)
	}
	return nil
}

// normalizeHex normalizes a hex string by stripping 0x prefix, padding with leading zeros, and adding 0x prefix back
func normalizeHex(input string, length int) string {
	hexStr := strings.TrimPrefix(input, "0x")
	// length-2 because we'll add "0x" prefix
	expectedHexLength := length - 2
	if len(hexStr) > expectedHexLength {
		// If input is longer than expected, return as-is to let validation catch it
		return input
	}
	paddedHex := strings.Repeat("0", expectedHexLength-len(hexStr)) + hexStr
	return "0x" + paddedHex
}

// removeWorkflow removes a workflow by its ID.
// removes both the workflow and its reference from the store.
func (s *workflowStore) removeWorkflow(workflowID string) error {
	if err := validateHexPrefixedField("workflowID", workflowID, expectedWorkflowIDLen); err != nil {
		return fmt.Errorf("invalid workflowID: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	w, exists := s.workflows[workflowID]
	if !exists {
		return fmt.Errorf("workflow with ID %s not found", workflowID)
	}
	w.close()
	delete(s.workflows, workflowID)
	delete(s.workflowReferenceToID, workflowReference{
		workflowOwner: w.workflowSelector.WorkflowOwner,
		workflowName:  w.workflowSelector.WorkflowName,
		workflowTag:   w.workflowSelector.WorkflowTag,
	})
	s.lggr.Debugf("Unregistered workflow %s", workflowID)
	return nil
}

func (s *workflowStore) getWorkflowByID(workflowID string) (*workflow, bool) {
	if err := validateHexPrefixedField("workflowID", workflowID, expectedWorkflowIDLen); err != nil {
		s.lggr.Debugf("Invalid workflowID in getWorkflowByID: %v", err)
		return nil, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	w, exists := s.workflows[workflowID]
	return w, exists
}

func (s *workflowStore) getWorkflowIDByReference(workflowOwner, workflowName, workflowTag string) (string, bool) {
	if err := validateHexPrefixedField("workflowOwner", workflowOwner, expectedWorkflowOwnerLen); err != nil {
		s.lggr.Debugf("Invalid workflowOwner in getWorkflowIDByReference: %v", err)
		return "", false
	}
	if err := validateHexPrefixedField("workflowName", workflowName, workflowNameHashLength); err != nil {
		s.lggr.Debugf("Invalid workflowName in getWorkflowIDByReference: %v", err)
		return "", false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	wID, exists := s.workflowReferenceToID[workflowReference{
		workflowOwner: workflowOwner,
		workflowName:  workflowName,
		workflowTag:   workflowTag,
	}]
	return wID, exists
}

func (s *workflowStore) getWorkflows() []*workflow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	workflows := make([]*workflow, 0, len(s.workflows))
	for _, w := range s.workflows {
		workflows = append(workflows, w)
	}
	return workflows
}

type workflow struct {
	mu               sync.Mutex
	workflowSelector gateway.WorkflowSelector
	authorizedKeys   map[gateway.AuthorizedKey]struct{}
	sendCh           chan<- capabilities.TriggerAndId[*http.Payload]
	closed           bool
	metadata         WorkflowRegistrationMetadata
}

func newWorkflow(workflowSelector gateway.WorkflowSelector, authorizedKeys []gateway.AuthorizedKey, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) *workflow {
	authorizedKeysMap := make(map[gateway.AuthorizedKey]struct{})
	for _, key := range authorizedKeys {
		authorizedKeysMap[key] = struct{}{}
	}
	return &workflow{
		workflowSelector: workflowSelector,
		authorizedKeys:   authorizedKeysMap,
		sendCh:           sendCh,
		closed:           false,
		metadata:         WorkflowRegistrationMetadata{},
	}
}

func newWorkflowWithMetadata(workflowSelector gateway.WorkflowSelector, authorizedKeys []gateway.AuthorizedKey, sendCh chan<- capabilities.TriggerAndId[*http.Payload], metadata WorkflowRegistrationMetadata) *workflow {
	authorizedKeysMap := make(map[gateway.AuthorizedKey]struct{})
	for _, key := range authorizedKeys {
		authorizedKeysMap[key] = struct{}{}
	}
	return &workflow{
		workflowSelector: workflowSelector,
		authorizedKeys:   authorizedKeysMap,
		sendCh:           sendCh,
		closed:           false,
		metadata:         metadata,
	}
}

func (w *workflow) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		close(w.sendCh)
		w.closed = true
	}
}

func (w *workflow) trigger(ctx context.Context, trigger capabilities.TriggerAndId[*http.Payload]) error {
	if trigger.Id == "" {
		return fmt.Errorf("trigger ID cannot be empty: %v", trigger)
	}
	select {
	case <-ctx.Done():
		return errContextCanceled
	default:
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errWorkflowClosed
	}
	select {
	case w.sendCh <- trigger:
		return nil
	default:
		return errFullChannel
	}
}
