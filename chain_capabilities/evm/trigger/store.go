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
	filterID string
	// physicalFilterID is the workflow-independent content hash of the filter's
	// physical matching criteria (chain selector + canonicalized addresses,
	// event sigs, and positional topics). It is the metering ResourceID and the
	// shared-resource refcount key, so the unregister, cleanup, and snapshot
	// paths all reuse it from here without the request input. Identical filters
	// registered by different triggers share one physicalFilterID and are billed
	// once (a +delta on the 0->1 activation, a -delta on the 1->0 release).
	physicalFilterID string
	// reservedAddressCount is the number of filter addresses this filter bills:
	// the +delta emitted on the physical filter's 0->1 activation, and the
	// -delta on its 1->0 release, both carry this value. UnregisterLogTrigger
	// ignores its request input, so the count is stashed here at registration.
	reservedAddressCount int64
	// donID is stashed from the registration RequestMetadata so the
	// unregister/cleanup/snapshot paths reproduce the same identity as the
	// activation delta without the original request. It is the resolved metering
	// DON ID string (capability DON, or the consumer WorkflowDonID fallback when
	// the host did not inject a capability DON); empty when neither is known.
	donID string
	// workflowOwner is stored for attribution.
	workflowOwner string
	// orgID is the organization ID resolved from workflowOwner at registration
	// time and stored alongside so that emit and snapshot paths can use it
	// without a network call.
	orgID string
	expressions   []query.Expression
	confidence    primitives.ConfidenceLevel
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
	WriteAndIsFirstForPhysical(triggerID string, value logTriggerState) (firstForPhysical bool)
	DeleteAndIsLastForPhysical(triggerID, physicalFilterID string) (found, lastForPhysical bool)
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

// WriteAndIsFirstForPhysical writes value under triggerID and reports whether,
// at the instant of the write, no OTHER trigger already held
// value.physicalFilterID. A true result is the 0->1 activation of that shared
// physical filter — the only transition that bills a +delta. Deriving the
// transition from owned state at operation time keeps the emitter stateless
// (no ledger). The scan and write are atomic under the store lock so two
// concurrent registrations of the same physical filter can never both observe
// zero and double-bill.
func (cs *logTriggerStore) WriteAndIsFirstForPhysical(triggerID string, value logTriggerState) (firstForPhysical bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	firstForPhysical = true
	for id, existing := range cs.triggers {
		if id == triggerID {
			continue
		}
		if existing.physicalFilterID == value.physicalFilterID {
			firstForPhysical = false
			break
		}
	}
	cs.triggers[triggerID] = value
	return firstForPhysical
}

// DeleteAndIsLastForPhysical deletes triggerID and reports whether any remaining
// trigger still holds physicalFilterID. lastForPhysical is true when none
// remain — the 1->0 deactivation that bills a -delta. found reports whether the
// trigger existed. Scan and delete are atomic under the store lock.
func (cs *logTriggerStore) DeleteAndIsLastForPhysical(triggerID, physicalFilterID string) (found, lastForPhysical bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	_, found = cs.triggers[triggerID]
	delete(cs.triggers, triggerID)
	lastForPhysical = true
	for _, existing := range cs.triggers {
		if existing.physicalFilterID == physicalFilterID {
			lastForPhysical = false
			break
		}
	}
	return found, lastForPhysical
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
