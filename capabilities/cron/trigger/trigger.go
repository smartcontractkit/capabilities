package trigger

import (
	"context"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

var _ capabilities.TriggerCapability = (*capability)(nil)

type Output struct {
	Timestamp string
}

type capability struct {
	logger logger.Logger
}

type Params struct {
	Logger logger.Logger
}

func New(p Params) *capability {
	return &capability{
		logger: p.Logger,
	}
}

func (c *capability) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo("cron-trigger@1.0.0", capabilities.CapabilityTypeTrigger, "Trigger based on a CRON schedule")

}

func (c *capability) RegisterTrigger(ctx context.Context, request capabilities.CapabilityRequest) (<-chan capabilities.CapabilityResponse, error) {
	result := make(chan capabilities.CapabilityResponse)

	c.logger.Debugf("Registering trigger to WorkflowID: %s", request.Metadata.WorkflowID)

	go func() {
		defer close(result)
		for {
			c.logger.Debugf("Producing a response for WorkflowID: %s", request.Metadata.WorkflowID)
			output := Output{
				Timestamp: time.Now().Format(time.RFC3339),
			}
			outputMap, err := values.NewMap(map[string]any{
				"timestamp": output.Timestamp,
			})

			if err != nil {
				return
			}

			c.logger.Debugf("output: %v", outputMap)

			result <- capabilities.CapabilityResponse{
				Value: outputMap,
				Err:   nil,
			}
			time.Sleep(1 * time.Second)
		}
	}()

	return result, nil
}

func (c *capability) UnregisterTrigger(ctx context.Context, request capabilities.CapabilityRequest) error {
	return nil
}
