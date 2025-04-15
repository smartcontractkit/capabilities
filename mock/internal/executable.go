package internal

import (
	"context"
	"errors"
	"time"

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
	requestChan    chan ExecutableRequest
	ResponseChan   chan capabilities.CapabilityResponse
	ExecuteTimeout time.Duration
}

func (t *Executable) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	return nil
}

func (t *Executable) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	return nil
}

func (t *Executable) Execute(ctx context.Context, request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	execReq := ExecutableRequest{
		ID:      t.ID,
		CapType: utils.ToMockServerEnum(t.CapabilityType),
		Request: request,
	}

	select {
	case t.requestChan <- execReq:
	case <-ctx.Done():
		return capabilities.CapabilityResponse{}, ctx.Err()
	case <-time.After(t.ExecuteTimeout):
		return capabilities.CapabilityResponse{}, errors.New("timeout sending execute request")
	}

	select {
	case d := <-t.ResponseChan:
		return d, nil
	case <-ctx.Done():
		return capabilities.CapabilityResponse{}, ctx.Err()
	case <-time.After(t.ExecuteTimeout):
		return capabilities.CapabilityResponse{}, errors.New("timeout waiting for execute response")
	}
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
		requestChan:    rChan,
		ResponseChan:   make(chan capabilities.CapabilityResponse),
		ExecuteTimeout: time.Minute * 5,
	}
}
