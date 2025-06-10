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

type LogTriggerStore struct {
	mu       sync.RWMutex
	triggers map[string]logTriggerState
}

type LogTriggerStoreI interface {
	Read(triggerID string) (value logTriggerState, ok bool)
	ReadAll() (values map[string]logTriggerState)
	Write(triggerID string, value logTriggerState)
	Delete(triggerID string)
}

var _ LogTriggerStoreI = (*LogTriggerStore)(nil)

func NewLogTriggerStore() *LogTriggerStore {
	return &LogTriggerStore{
		triggers: map[string]logTriggerState{},
	}
}

func (cs *LogTriggerStore) Read(triggerID string) (value logTriggerState, ok bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	trigger, ok := cs.triggers[triggerID]
	return trigger, ok
}

func (cs *LogTriggerStore) ReadAll() (values map[string]logTriggerState) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	tCopy := map[string]logTriggerState{}
	for key, value := range cs.triggers {
		tCopy[key] = value
	}
	return tCopy
}

func (cs *LogTriggerStore) Write(triggerID string, value logTriggerState) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.triggers[triggerID] = value
}

func (cs *LogTriggerStore) Update(triggerID string, lastBlock *big.Int) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	trigger, ok := cs.triggers[triggerID]
	if !ok {
		return fmt.Errorf("cannot find trigger with ID %q", triggerID)
	}
	cs.triggers[triggerID] = logTriggerState{
		cancelFunc: trigger.cancelFunc,
		lastBlock:  lastBlock,
		//logCh:      trigger.logCh,
	}
	return nil
}

func (cs *LogTriggerStore) Delete(triggerID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.triggers, triggerID)
}
