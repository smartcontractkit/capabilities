package checks

import (
	"github.com/smartcontractkit/capabilities/capabilitywatcher/internal"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
)

var _ internal.ExecutableChecker = (*HttpActionChecker)(nil)

type HttpActionChecker struct {
}

func (r HttpActionChecker) CreateRegisterToWorkflowRequest() (capabilities.RegisterToWorkflowRequest, error) {
	return capabilities.RegisterToWorkflowRequest{}, nil // NOOP
}

func (r HttpActionChecker) CreateUnregisterFromWorkflowRequest() (capabilities.UnregisterFromWorkflowRequest, error) {
	return capabilities.UnregisterFromWorkflowRequest{}, nil // NOOP
}

func (r HttpActionChecker) CreateExecuteRequest() (capabilities.CapabilityRequest, error) {
	payload, err := anypb.New(&http.Request{
		Url:       "https://httpbin.org/get?somerandomnumber=1234",
		Method:    "GET",
		TimeoutMs: 10000,
	})
	if err != nil {
		return capabilities.CapabilityRequest{}, err
	}
	return capabilities.CapabilityRequest{
		Metadata: capabilities.RequestMetadata{
			WorkflowID: "capabilitywatcher",
		},
		Config:       nil,
		Payload:      payload,
		Method:       "SendRequest",
		CapabilityId: "http-actions@1.0.0-alpha",
	}, nil
}

func (r HttpActionChecker) Assert(response capabilities.CapabilityResponse) {
	return
}
