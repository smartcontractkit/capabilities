package internal

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/smartcontractkit/capabilities/mock/internal/pb"
	utils2 "github.com/smartcontractkit/capabilities/mock/utils"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ capabilities.TriggerCapability = (*Trigger)(nil)

type subscriber struct {
	Ch         chan<- capabilities.TriggerResponse
	WorkflowID string
}
type Trigger struct {
	capabilities.CapabilityInfo
	Subscribers map[string]*subscriber
	lggr        logger.Logger
	mu          sync.RWMutex
}

func NewTrigger(info *pb.CapabilityInfo, lggr logger.Logger) *Trigger {
	return &Trigger{
		CapabilityInfo: capabilities.CapabilityInfo{
			ID:             info.ID,
			CapabilityType: utils2.ToCapabilityEnum(info.CapabilityType),
			Description:    info.Description,
			DON:            nil,
			IsLocal:        info.IsLocal,
		},
		lggr:        lggr,
		Subscribers: make(map[string]*subscriber),
		mu:          sync.RWMutex{},
	}
}

func (m *Trigger) RegisterTrigger(_ context.Context, request capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.Subscribers[request.TriggerID]; exists {
		m.lggr.Warnw("Trigger already registered", "TriggerID", request.TriggerID, "timestamp", time.Now().String())
		return nil, fmt.Errorf("triggerId %s already registered", request.TriggerID)
	}
	ch := make(chan capabilities.TriggerResponse, 1000)
	m.Subscribers[request.TriggerID] = &subscriber{
		Ch:         ch,
		WorkflowID: request.TriggerID,
	}
	m.lggr.Infow("New subscriber registered", "id", m.ID, "nrOfSubs", len(m.Subscribers), "triggerID", request.TriggerID, "timestamp", time.Now().String())

	return ch, nil
}

func (m *Trigger) UnregisterTrigger(ctx context.Context, request capabilities.TriggerRegistrationRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.Subscribers[request.TriggerID]; exists {
		close(m.Subscribers[request.TriggerID].Ch)
		delete(m.Subscribers, request.TriggerID)
	}
	m.lggr.Infow("Subscriber unregistered", "TriggerID", request.TriggerID, "nrOfSubs", len(m.Subscribers), "timestamp", time.Now().String())

	return nil
}

func (m *Trigger) AckEvent(_ context.Context, triggerID string, eventID string, method string) error {
	m.lggr.Debugw("Trigger event acknowledged", "triggerID", triggerID, "eventID", eventID, "method", method)
	return nil
}
