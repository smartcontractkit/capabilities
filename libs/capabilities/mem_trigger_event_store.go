package capabilities

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type MemEventStore struct {
	mu   sync.Mutex
	recs map[string]map[string]PendingEvent // triggerID -> eventID -> event
}

func NewMemEventStore() *MemEventStore {
	return &MemEventStore{
		recs: make(map[string]map[string]PendingEvent),
	}
}

func (m *MemEventStore) Insert(ctx context.Context, r PendingEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	eventsForTrigger := m.recs[r.TriggerId]
	if eventsForTrigger == nil {
		eventsForTrigger = make(map[string]PendingEvent)
		m.recs[r.TriggerId] = eventsForTrigger
	}
	eventsForTrigger[r.EventId] = r
	return nil
}

func (m *MemEventStore) UpdateDelivery(ctx context.Context, triggerID string, eventID string, lastSentAt time.Time, attempts int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	eventsForTrigger := m.recs[triggerID]
	if eventsForTrigger == nil {
		return fmt.Errorf("event not found trigger=%s event=%s", triggerID, eventID)
	}

	rec, ok := eventsForTrigger[eventID]
	if !ok {
		return fmt.Errorf("event not found trigger=%s event=%s", triggerID, eventID)
	}

	rec.Attempts = attempts
	rec.LastSentAt = lastSentAt
	eventsForTrigger[eventID] = rec
	return nil
}

func (m *MemEventStore) DeleteEvent(ctx context.Context, triggerID, eventID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	eventsForTrigger := m.recs[triggerID]
	if eventsForTrigger == nil {
		return nil
	}
	delete(eventsForTrigger, eventID)
	if len(eventsForTrigger) == 0 {
		delete(m.recs, triggerID)
	}
	return nil
}

func (m *MemEventStore) DeleteEventsForTrigger(ctx context.Context, triggerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.recs, triggerID)
	return nil
}

func (m *MemEventStore) List(ctx context.Context) ([]PendingEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]PendingEvent, 0)
	for _, eventsForTrigger := range m.recs {
		for _, r := range eventsForTrigger {
			out = append(out, r)
		}
	}
	return out, nil
}
