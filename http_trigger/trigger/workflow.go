package trigger

import (
	"context"
	"fmt"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
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
func (s *workflowStore) upsertWorkflow(w *workflow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	workflowID, exists := s.workflowReferenceToID[workflowReference{
		workflowOwner: w.workflowSelector.WorkflowOwner,
		workflowName:  w.workflowSelector.WorkflowName,
		workflowTag:   w.workflowSelector.WorkflowTag,
	}]
	if exists {
		s.lggr.Debugw("Updating existing workflow reference %s/%s/%s. Removing previous workflow %s",
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
}

// removeWorkflow removes a workflow by its ID.
// removes both the workflow and its reference from the store.
func (s *workflowStore) removeWorkflow(workflowID string) error {
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, exists := s.workflows[workflowID]
	return w, exists
}

func (s *workflowStore) getWorkflowIDByReference(workflowOwner, workflowName, workflowTag string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wID, exists := s.workflowReferenceToID[workflowReference{
		workflowOwner: workflowOwner,
		workflowName:  workflowName,
		workflowTag:   workflowTag,
	}]
	return wID, exists
}

type workflow struct {
	mu               sync.Mutex
	workflowSelector gateway.WorkflowSelector
	authorizedKeys   map[string]struct{}
	sendCh           chan<- capabilities.TriggerAndId[*http.Payload]
	closed           bool
}

func newWorkflow(workflowSelector gateway.WorkflowSelector, authorizedKeys map[string]struct{}, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) *workflow {
	return &workflow{
		workflowSelector: workflowSelector,
		authorizedKeys:   authorizedKeys,
		sendCh:           sendCh,
		closed:           false,
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
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errWorkflowClosed
	}
	select {
	case <-ctx.Done():
		return errContextCanceled
	case w.sendCh <- trigger:
		return nil
	default:
		return errFullChannel
	}
}
