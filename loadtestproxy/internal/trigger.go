package internal

import (
	"context"

	"github.com/smartcontractkit/capabilities/loadtestproxy/internal/pb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

var _ capabilities.TriggerCapability = (*trigger)(nil)
var _ capabilities.TriggerExecutable = (*trigger)(nil)

type trigger struct {
	*capabilityInfo
	tChan chan capabilities.TriggerResponse
}

func NewTrigger(info *pb.CapabilityInfo) *trigger {
	return &trigger{
		capabilityInfo: &capabilityInfo{info: capabilities.CapabilityInfo{
			ID:             info.ID,
			CapabilityType: toRemoteCapEnum(info.CapabilityType),
			Description:    info.Description,
			DON:            nil,
			IsLocal:        info.IsLocal,
		}},
		tChan: make(chan capabilities.TriggerResponse, 1000),
	}
}

func (m *trigger) RegisterTrigger(ctx context.Context, request capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	//TODO @george-dorin: every subscriber should have it's onw chan and we should save the config?
	return m.tChan, nil
}

func (m *trigger) UnregisterTrigger(ctx context.Context, request capabilities.TriggerRegistrationRequest) error {
	return nil
}
