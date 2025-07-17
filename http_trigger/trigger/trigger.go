package trigger

import (
	"context"
	"encoding/json"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const ServiceName = "HTTPTriggerCapability"
const WorkflowRegistryContractName = "WorkflowRegistryV2"
const defaultSendChannelBufferSize = uint32(1000)

var _ server.HTTPCapability = &service{}

type ServiceConfig struct {
	SendChannelBufferSize uint32 `json:"sendChannelBufferSize"`
	// RateLimiter configuration for messages incoming to this node from the gateway.
	// The sender is a Gateway node, which is identified by the Gateway ID.
	RateLimiter ratelimit.RateLimiterConfig `json:"incomingRateLimiter" `
	// OutgoingRateLimiter is the configuration for outgoing messages from this node to the gateway.
	// The sender is a workflow owner
	OutgoingRateLimiter ratelimit.RateLimiterConfig `json:"outgoingRateLimiter"`
}

type ConnectorHandler interface {
	services.Service
	RegisterWorkflow(ctx context.Context, workflowSelector gateway.WorkflowSelector, input *http.Config, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error
	UnregisterWorkflow(ctx context.Context, workflowID string) error
}

type service struct {
	services.StateMachine
	lggr             logger.SugaredLogger
	cfg              ServiceConfig
	connectorHandler ConnectorHandler
}

func NewService(lggr logger.Logger) *service {
	return &service{
		lggr: logger.Sugared(logger.Named(lggr, ServiceName)),
	}
}

func (s *service) Initialise(
	ctx context.Context,
	config string,
	_ core.TelemetryService,
	_ core.KeyValueStore,
	_ core.ErrorLog,
	_ core.PipelineRunnerService,
	_ core.RelayerSet,
	_ core.OracleFactory,
	gc core.GatewayConnector,
	_ core.Keystore,
) error {
	s.lggr.Debugf("Initialising %s", ServiceName)

	var serviceConfig ServiceConfig
	err := json.Unmarshal([]byte(config), &serviceConfig)
	if err != nil {
		return err
	}
	s.cfg = serviceConfig
	s.connectorHandler, err = NewConnectorHandler(s.lggr, gc, serviceConfig)
	if err != nil {
		return err
	}
	return nil
}

func (s *service) Start(ctx context.Context) error {
	s.lggr.Debug("Service starting...")
	return s.StartOnce(ServiceName, func() error {
		return s.connectorHandler.Start(ctx)
	})
}

func (s *service) Close() error {
	s.lggr.Debug("Service closing...")
	return s.StopOnce(ServiceName, func() error {
		return s.connectorHandler.Close()
	})
}

func (s *service) HealthReport() map[string]error {
	return map[string]error{s.Name(): s.Healthy()}
}

func (s *service) Ready() error {
	return s.StateMachine.Healthy()
}

func (s *service) Name() string {
	return ServiceName
}

func (s *service) Description() string {
	return "HTTP Trigger Service"
}

func (s *service) RegisterTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *http.Config) (<-chan capabilities.TriggerAndId[*http.Payload], error) {
	sendChannelBufferSize := s.cfg.SendChannelBufferSize
	if sendChannelBufferSize == 0 {
		sendChannelBufferSize = defaultSendChannelBufferSize
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], sendChannelBufferSize)
	workflowSelector := gateway.WorkflowSelector{
		WorkflowID:    metadata.WorkflowID,
		WorkflowOwner: metadata.WorkflowOwner,
		WorkflowName:  metadata.WorkflowName,
		WorkflowTag:   metadata.WorkflowTag,
	}
	err := s.connectorHandler.RegisterWorkflow(ctx, workflowSelector, input, sendCh)
	if err != nil {
		return nil, err
	}
	return sendCh, nil
}

func (s *service) UnregisterTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *http.Config) error {
	err := s.connectorHandler.UnregisterWorkflow(ctx, metadata.WorkflowID)
	if err != nil {
		s.lggr.Errorf("Failed to unregister workflow %s: %v", metadata.WorkflowID, err)
	}
	return err
}
