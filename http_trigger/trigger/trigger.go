package trigger

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

const ServiceName = "HTTPTriggerCapability"

var _ server.HTTPCapability = &service{}

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
	metrics          *Metrics
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
	kvstore core.KeyValueStore,
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
	s.cfg = applyDefaults(serviceConfig)
	outgoingRateLimiter, err := ratelimit.NewRateLimiter(s.cfg.OutgoingRateLimiter)
	if err != nil {
		return err
	}
	incomingRateLimiter, err := ratelimit.NewRateLimiter(s.cfg.IncomingRateLimiter)
	if err != nil {
		return err
	}
	workflowStore := newWorkflowStore(s.lggr)
	metadataPublisher := NewGatewayMetadataPublisher(s.lggr, gc, outgoingRateLimiter, workflowStore, s.cfg)
	requestCache := newRequestCache(s.lggr, kvstore, time.Duration(s.cfg.RequestCacheTTL)*time.Second)
	s.connectorHandler, err = NewConnectorHandler(s.lggr, gc, s.cfg, outgoingRateLimiter, incomingRateLimiter, workflowStore, metadataPublisher, requestCache, s.metrics)
	if err != nil {
		return err
	}
	s.metrics, err = NewMetrics()
	if err != nil {
		return err
	}
	return s.Start(ctx)
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
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], s.cfg.SendChannelBufferSize)
	// TODO: remove this when testing frameworks (local CRE, capabilities integration tests framework) migrate to WR v2
	if metadata.WorkflowTag == "" {
		metadata.WorkflowTag = "TEMP_TAG"
	}
	workflowSelector := gateway.WorkflowSelector{
		WorkflowID:    strings.ToLower(ensureHexPrefix(metadata.WorkflowID)),
		WorkflowOwner: strings.ToLower(ensureHexPrefix(metadata.WorkflowOwner)),
		WorkflowName:  strings.ToLower(ensureHexPrefix(metadata.WorkflowName)),
		WorkflowTag:   metadata.WorkflowTag,
	}
	err := s.connectorHandler.RegisterWorkflow(ctx, workflowSelector, input, sendCh)
	if err != nil {
		s.metrics.IncrementRegisterFailureCount(ctx, s.lggr)
		return nil, err
	}
	s.metrics.IncrementRegisterCount(ctx, s.lggr)
	return sendCh, nil
}

func (s *service) UnregisterTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *http.Config) error {
	err := s.connectorHandler.UnregisterWorkflow(ctx, metadata.WorkflowID)
	if err != nil {
		s.lggr.Errorf("Failed to unregister workflow %s: %v", metadata.WorkflowID, err)
		s.metrics.IncrementDeregisterFailureCount(ctx, s.lggr)
		return err
	}
	s.metrics.IncrementDeregisterCount(ctx, s.lggr)
	return nil
}

func ensureHexPrefix(s string) string {
	if len(s) >= 2 && s[:2] == "0x" {
		return s
	}
	return "0x" + s
}
