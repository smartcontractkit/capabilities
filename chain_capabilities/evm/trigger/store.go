package trigger

import (
	"context"
	"fmt"
	"maps"
	"math/big"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
)

type filter struct {
	filterID    string
	expressions []query.Expression
	confidence  primitives.ConfidenceLevel
}

type logTriggerState struct {
	cancelFunc context.CancelFunc
	lastBlock  *big.Int //latest finalized block number that this trigger is aware of.
	/*
		unfinalizedSentEventIDs is a map of event IDs that prevent log trigger of sending duplicate unfinalized events.
		Once the lastBlocks moves ahead of the block that contains the event, the event ID can be removed from this map.
	*/
	unfinalizedSentEventIDs map[string]*big.Int
	filter
}

type logTriggerStore struct {
	mu       sync.RWMutex
	triggers map[string]logTriggerState
}

// LogTriggerStore is an interface for managing locking/unlocking of log triggers, and it also allows to inject it from a test context.
type LogTriggerStore interface {
	Read(triggerID string) (value logTriggerState, ok bool)
	ReadAll() (values map[string]logTriggerState)
	Write(triggerID string, value logTriggerState)
	Update(triggerID string, lastBlock *big.Int, unfinalizedSentEventIDs map[string]*big.Int) error
	Delete(triggerID string)
}

var _ LogTriggerStore = (*logTriggerStore)(nil)

func NewLogTriggerStore() LogTriggerStore {
	return &logTriggerStore{
		triggers: map[string]logTriggerState{},
	}
}

func (cs *logTriggerStore) Read(triggerID string) (value logTriggerState, ok bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	trigger, ok := cs.triggers[triggerID]
	return trigger, ok
}

func (cs *logTriggerStore) ReadAll() (values map[string]logTriggerState) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	tCopy := map[string]logTriggerState{}
	maps.Copy(tCopy, cs.triggers)
	return tCopy
}

func (cs *logTriggerStore) Write(triggerID string, value logTriggerState) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.triggers[triggerID] = value
}

func (cs *logTriggerStore) Update(triggerID string, lastBlock *big.Int, unfinalizedSentEventIDs map[string]*big.Int) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	trigger, ok := cs.triggers[triggerID]
	if !ok {
		return fmt.Errorf("cannot find trigger with ID %q", triggerID)
	}
	cs.triggers[triggerID] = logTriggerState{
		cancelFunc:              trigger.cancelFunc,
		lastBlock:               lastBlock,
		unfinalizedSentEventIDs: unfinalizedSentEventIDs,
		filter:                  trigger.filter,
	}
	return nil
}

func (cs *logTriggerStore) Delete(triggerID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.triggers, triggerID)
}
