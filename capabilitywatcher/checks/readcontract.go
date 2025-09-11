package checks

import (
	"github.com/smartcontractkit/capabilities/capabilitywatcher/internal"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

var _ internal.ExecutableChecker = (*ReadContractChecker)(nil)

type ReadContractChecker struct {
}

func (r ReadContractChecker) CreateRegisterToWorkflowRequest() (capabilities.RegisterToWorkflowRequest, error) {
	// TODO implement me
	panic("implement me")
}

func (r ReadContractChecker) CreateUnregisterFromWorkflowRequest() (capabilities.UnregisterFromWorkflowRequest, error) {
	// TODO implement me
	panic("implement me")
}

func (r ReadContractChecker) CreateExecuteRequest() (capabilities.CapabilityRequest, error) {
	// TODO implement me
	panic("implement me")
}

func (r ReadContractChecker) Assert(response capabilities.CapabilityResponse) {
	// TODO implement me
	panic("implement me")
}
