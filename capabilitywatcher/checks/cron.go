package checks

import (
	"github.com/google/uuid"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/capabilities/capabilitywatcher/internal"
)

var _ internal.TriggerChecker = (*CronChecker)(nil)

type CronChecker struct {
}

func (c CronChecker) NewRegistrationRequest() (capabilities.TriggerRegistrationRequest, error) {
	payload, err := anypb.New(&cron.Config{Schedule: "*/30 * * * * *"})
	if err != nil {
		return capabilities.TriggerRegistrationRequest{}, err
	}
	return capabilities.TriggerRegistrationRequest{
		TriggerID: uuid.New().String(),
		Metadata: capabilities.RequestMetadata{
			WorkflowID: "healthcheck",
		},
		Config:  nil,
		Payload: payload,
		Method:  "",
	}, nil
}

func (c CronChecker) Assert(_ capabilities.TriggerResponse) {
}
