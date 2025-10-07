package checks

import (
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/capabilities/capabilitywatcher/internal"
)

var _ internal.ExecutableChecker = (*HTTPActionChecker)(nil)

type HTTPActionChecker struct {
}

func (r HTTPActionChecker) CreateRegisterToWorkflowRequest() (capabilities.RegisterToWorkflowRequest, error) {
	return capabilities.RegisterToWorkflowRequest{}, nil // NOOP
}

func (r HTTPActionChecker) CreateUnregisterFromWorkflowRequest() (capabilities.UnregisterFromWorkflowRequest, error) {
	return capabilities.UnregisterFromWorkflowRequest{}, nil // NOOP
}

func (r HTTPActionChecker) CreateExecuteRequest() (capabilities.CapabilityRequest, error) {
	payload, err := anypb.New(&http.Request{
		Url:     "https://httpbin.org/get?somerandomnumber=1234",
		Method:  "GET",
		Timeout: durationpb.New(25 * time.Second),
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

func (r HTTPActionChecker) Assert(response capabilities.CapabilityResponse) {
}
