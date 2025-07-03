package trigger

import (
	"context"
	"fmt"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
)

var (
	errWorkflowClosed  = fmt.Errorf("workflow is closed, cannot send trigger")
	errContextCanceled = fmt.Errorf("context canceled, cannot send trigger")
	errFullChannel     = fmt.Errorf("workflowID channel is full, cannot send trigger")
)

type workflow struct {
	mu             sync.Mutex
	authorizedKeys map[string]struct{}
	sendCh         chan<- capabilities.TriggerAndId[*http.Payload]
	closed         bool
}

func newWorkflow(authorizedKeys map[string]struct{}, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) *workflow {
	return &workflow{
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
