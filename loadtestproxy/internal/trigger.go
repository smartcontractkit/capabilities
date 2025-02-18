package internal

import (
	"context"
	"sync"

	"github.com/smartcontractkit/capabilities/loadtestproxy/internal/pb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ capabilities.TriggerCapability = (*trigger)(nil)
var _ capabilities.TriggerExecutable = (*trigger)(nil)

type subscriber struct {
	Ch         chan capabilities.TriggerResponse
	WorkflowID string
}
type trigger struct {
	*capabilityInfo
	subscribers map[string]*subscriber
	lggr        logger.Logger
	mu          sync.RWMutex
}

func NewTrigger(info *pb.CapabilityInfo, lggr logger.Logger) *trigger {
	return &trigger{
		capabilityInfo: &capabilityInfo{info: capabilities.CapabilityInfo{
			ID:             info.ID,
			CapabilityType: ToCapabilityEnum(info.CapabilityType),
			Description:    info.Description,
			DON:            nil,
			IsLocal:        info.IsLocal,
		}},
		lggr:        lggr,
		subscribers: make(map[string]*subscriber),
		mu:          sync.RWMutex{},
	}
}

func (m *trigger) RegisterTrigger(ctx context.Context, request capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.subscribers[request.TriggerID]; exists {
		m.lggr.Warnw("Trigger already registered", "TriggerID", request.TriggerID)
		return m.subscribers[request.TriggerID].Ch, nil
	}
	m.subscribers[request.TriggerID] = &subscriber{
		Ch:         make(chan capabilities.TriggerResponse, 1000),
		WorkflowID: request.TriggerID,
	}
	m.lggr.Infow("New subscriber registered", "id", m.info.ID, "nrOfSubs", len(m.subscribers))
	return m.subscribers[request.TriggerID].Ch, nil
}

func (m *trigger) UnregisterTrigger(ctx context.Context, request capabilities.TriggerRegistrationRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.subscribers[request.TriggerID]; exists {
		delete(m.subscribers, request.TriggerID)
	}
	m.lggr.Infow("Subscriber unregistered", "TriggerID", request.TriggerID, "nrOfSubs", len(m.subscribers))

	return nil
}
