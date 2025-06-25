package trigger

import (
	"context"
	"fmt"
	"math/big"
	"sync"
)

type logTriggerState struct {
	cancelFunc context.CancelFunc
	lastBlock  *big.Int
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
	Update(triggerID string, lastBlock *big.Int) error
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
	for key, value := range cs.triggers {
		tCopy[key] = value
	}
	return tCopy
}

func (cs *logTriggerStore) Write(triggerID string, value logTriggerState) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.triggers[triggerID] = value
}

func (cs *logTriggerStore) Update(triggerID string, lastBlock *big.Int) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	trigger, ok := cs.triggers[triggerID]
	if !ok {
		return fmt.Errorf("cannot find trigger with ID %q", triggerID)
	}
	cs.triggers[triggerID] = logTriggerState{
		cancelFunc: trigger.cancelFunc,
		lastBlock:  lastBlock,
	}
	return nil
}

func (cs *logTriggerStore) Delete(triggerID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.triggers, triggerID)
}
