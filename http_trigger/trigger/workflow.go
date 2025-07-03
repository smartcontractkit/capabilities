package trigger

import (
	"context"
	"fmt"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var (
	errWorkflowClosed  = fmt.Errorf("workflow is closed, cannot send trigger")
	errContextCanceled = fmt.Errorf("context canceled, cannot send trigger")
	errFullChannel     = fmt.Errorf("workflowID channel is full, cannot send trigger")
)

type workflow struct {
	workflowID     string
	mu             sync.Mutex
	authorizedKeys map[string]AuthorizedKey
	sendCh         chan<- capabilities.TriggerAndId[*http.Payload]
	closed         bool
}

func newWorkflow(workflowID string, authorizedKeys map[string]AuthorizedKey, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) *workflow {
	return &workflow{
		workflowID:     workflowID,
		authorizedKeys: authorizedKeys,
		sendCh:         sendCh,
		closed:         false,
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

type WorkflowStore interface {
	RegisterWorkflow(workflowID string, authorizedKeys []AuthorizedKey, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error
	UnregisterWorkflow(workflowID string) error
	GetWorkflow(workflowID string) (*workflow, error)
	GetWorkflows() ([]*workflow, error)
}

type workflowStore struct {
	workflowsMu sync.RWMutex
	workflows   map[string]*workflow // workflowID -> workflow metadata
	lggr        logger.Logger
}

func NewWorkflowStore(lggr logger.Logger) *workflowStore {
	return &workflowStore{
		workflows: make(map[string]*workflow),
		lggr:      logger.Named(lggr, "WorkflowStore"),
	}
}

func (s *workflowStore) RegisterWorkflow(workflowID string, authorizedKeys []AuthorizedKey, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error {
	s.workflowsMu.Lock()
	defer s.workflowsMu.Unlock()
	if _, exists := s.workflows[workflowID]; exists {
		s.lggr.Debugw("Workflow already registered, re-registering", "workflowID", workflowID)
	}
	keys := make(map[string]AuthorizedKey, len(authorizedKeys))
	for _, key := range authorizedKeys {
		keys[key.PublicKey] = AuthorizedKey{
			KeyType:   key.KeyType,
			PublicKey: key.PublicKey,
		}
	}
	s.workflows[workflowID] = newWorkflow(workflowID, keys, sendCh)
	s.lggr.Debugf("Registered workflow %s", workflowID)
	return nil
}

func (s *workflowStore) UnregisterWorkflow(workflowID string) error {
	s.workflowsMu.Lock()
	defer s.workflowsMu.Unlock()
	if workflow, exists := s.workflows[workflowID]; exists {
		workflow.close()
		delete(s.workflows, workflowID)
		s.lggr.Debugf("Unregistered workflow %s", workflowID)
		return nil
	}
	return fmt.Errorf("workflow %s not found", workflowID)
}

func (s *workflowStore) GetWorkflow(workflowID string) (*workflow, error) {
	s.workflowsMu.RLock()
	defer s.workflowsMu.RUnlock()
	if workflow, exists := s.workflows[workflowID]; exists {
		return workflow, nil
	}
	return nil, fmt.Errorf("workflow %s not found", workflowID)
}

func (s *workflowStore) GetWorkflows() ([]*workflow, error) {
	s.workflowsMu.RLock()
	defer s.workflowsMu.RUnlock()
	workflows := make([]*workflow, 0, len(s.workflows))
	for _, wf := range s.workflows {
		workflows = append(workflows, wf)
	}
	return workflows, nil
}
