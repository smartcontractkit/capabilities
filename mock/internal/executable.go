package internal

import (
	"context"

	"github.com/smartcontractkit/capabilities/mock/internal/pb"
	"github.com/smartcontractkit/capabilities/mock/utils"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

var _ capabilities.TargetCapability = (*Executable)(nil)
var _ capabilities.ExecutableCapability = (*Executable)(nil)

type ExecutableRequest struct {
	ID      string
	CapType pb.CapabilityType
	Request capabilities.CapabilityRequest
}
type Executable struct {
	capabilities.CapabilityInfo
	requestChan  chan ExecutableRequest
	ResponseChan chan capabilities.CapabilityResponse
}

func (t *Executable) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	return nil
}

func (t *Executable) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	return nil
}

func (t *Executable) Execute(ctx context.Context, request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	t.requestChan <- ExecutableRequest{
		ID:      t.ID,
		CapType: utils.ToMockServerEnum(t.CapabilityType),
		Request: request,
	}

	//TODO: @george-dorin add timeout
	//TODO: @george-dorin, might be good to sequence the response
	d := <-t.ResponseChan
	return d, nil
}

func NewExecutable(info *pb.CapabilityInfo, rChan chan ExecutableRequest) *Executable {
	return &Executable{
		CapabilityInfo: capabilities.CapabilityInfo{
			ID:             info.ID,
			CapabilityType: utils.ToCapabilityEnum(info.CapabilityType),
			Description:    info.Description,
			DON:            nil,
			IsLocal:        info.IsLocal,
		},
		requestChan:  rChan,
		ResponseChan: make(chan capabilities.CapabilityResponse),
	}
}
