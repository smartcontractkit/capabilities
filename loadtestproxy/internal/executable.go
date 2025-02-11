package internal

import (
	"context"

	"github.com/smartcontractkit/capabilities/loadtestproxy/internal/pb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

var _ capabilities.TargetCapability = (*executable)(nil)
var _ capabilities.ExecutableCapability = (*executable)(nil)

type executable struct {
	*capabilityInfo
	requestChan  chan ExecutableRequest
	responseChan chan capabilities.CapabilityResponse
}

func (t *executable) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	return nil
}

func (t *executable) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	return nil
}

func (t *executable) Execute(ctx context.Context, request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	t.requestChan <- ExecutableRequest{
		ID:      t.info.ID,
		capType: toLocalCapEnum(t.info.CapabilityType),
		request: request,
	}

	//TODO: @george-dorin add timeout
	//TODO: @george-dorin, might be good to sequence the response
	d := <-t.responseChan
	return d, nil
}

func NewExecutable(info *pb.CapabilityInfo, rChan chan ExecutableRequest) *executable {
	return &executable{
		capabilityInfo: &capabilityInfo{info: capabilities.CapabilityInfo{
			ID:             info.ID,
			CapabilityType: toRemoteCapEnum(info.CapabilityType),
			Description:    info.Description,
			DON:            nil,
			IsLocal:        info.IsLocal,
		}},
		requestChan:  rChan,
		responseChan: make(chan capabilities.CapabilityResponse),
	}
}
