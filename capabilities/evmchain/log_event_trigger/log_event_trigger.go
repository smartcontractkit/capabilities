package log_event_trigger

import (
	"context"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

var _ capabilities.TriggerCapability = (*capability)(nil)

type Output struct {
	Timestamp string
}

type capability struct {
	logger     logger.Logger
	relayerSet core.RelayerSet
}

type Params struct {
	Logger     logger.Logger
	RelayerSet core.RelayerSet
}

func New(p Params) *capability {
	return &capability{
		logger:     p.Logger,
		relayerSet: p.RelayerSet,
	}
}

func (c *capability) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo("log-event-trigger@1.0.0", capabilities.CapabilityTypeTrigger, "Trigger based on a transaction log events")
}

func (c *capability) RegisterTrigger(ctx context.Context, request capabilities.CapabilityRequest) (<-chan capabilities.CapabilityResponse, error) {
	result := make(chan capabilities.CapabilityResponse)

	c.logger.Debugf("Registering EVMChain:LogEventTrigger to WorkflowID: %s", request.Metadata.WorkflowID)

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
