package trigger

import (
	"maps"
	"sync"
)

type cronStore struct {
	mu       sync.RWMutex
	triggers map[string]cronTrigger
}

type CronStore interface {
	Read(triggerID string) (value cronTrigger, ok bool)
	ReadAll() (values map[string]cronTrigger)
	Write(triggerID string, value cronTrigger)
	WriteIfPresent(triggerID string, value cronTrigger) (written bool)
	Delete(triggerID string)
}

var _ CronStore = (CronStore)(nil)

func NewCronStore() *cronStore {
	return &cronStore{
		triggers: map[string]cronTrigger{},
	}
}

func (cs *cronStore) Read(triggerID string) (value cronTrigger, ok bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	trigger, ok := cs.triggers[triggerID]
	return trigger, ok
}

func (cs *cronStore) ReadAll() (values map[string]cronTrigger) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	tCopy := map[string]cronTrigger{}
	maps.Copy(tCopy, cs.triggers)
	return tCopy
}

func (cs *cronStore) Write(triggerID string, value cronTrigger) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.triggers[triggerID] = value
}

// WriteIfPresent updates triggerID only when it currently exists, performing
// the existence check and the write atomically under the store lock. It returns
// false without writing when the trigger has already been deleted (e.g. by a
// concurrent UnregisterTrigger). The cron task callback uses this so a tick that
// began before an unregister cannot re-insert ("resurrect") a trigger that was
// just removed: snapshot absence is the release signal, so a resurrected entry
// would keep the resource billed after the caller stopped it.
func (cs *cronStore) WriteIfPresent(triggerID string, value cronTrigger) (written bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if _, ok := cs.triggers[triggerID]; !ok {
		return false
	}
	cs.triggers[triggerID] = value
	return true
}

func (cs *cronStore) Delete(triggerID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.triggers, triggerID)
}
