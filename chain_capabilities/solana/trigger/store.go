package trigger

import (
	"sync"

	solanacappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
)

type solanaLogTriggerState struct {
	stopPolling func()
	filter      *solanacappb.FilterLogTriggerRequest
}

type solanaLogTriggerStore struct {
	mu       sync.RWMutex
	triggers map[string]solanaLogTriggerState
}

// SolanaLogTriggerStore is an interface for managing Solana log triggers.
// It allows storing and retrieving trigger state for cleanup and management.
type SolanaLogTriggerStore interface {
	Read(triggerID string) (value solanaLogTriggerState, ok bool)
	ReadAll() (values map[string]solanaLogTriggerState)
	Write(triggerID string, value solanaLogTriggerState)
	Delete(triggerID string)
}

var _ SolanaLogTriggerStore = (*solanaLogTriggerStore)(nil)

func NewSolanaLogTriggerStore() SolanaLogTriggerStore {
	return &solanaLogTriggerStore{
		triggers: map[string]solanaLogTriggerState{},
	}
}

func (s *solanaLogTriggerStore) Read(triggerID string) (value solanaLogTriggerState, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	trigger, ok := s.triggers[triggerID]
	return trigger, ok
}

func (s *solanaLogTriggerStore) ReadAll() (values map[string]solanaLogTriggerState) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tCopy := map[string]solanaLogTriggerState{}
	for k, v := range s.triggers {
		tCopy[k] = v
	}
	return tCopy
}

func (s *solanaLogTriggerStore) Write(triggerID string, value solanaLogTriggerState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.triggers[triggerID] = value
}

func (s *solanaLogTriggerStore) Delete(triggerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.triggers, triggerID)
}
