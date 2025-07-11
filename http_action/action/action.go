package action

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/smartcontractkit/capabilities/http_action/common"
	"github.com/smartcontractkit/capabilities/http_action/gateway"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const ServiceName = "HTTPActionCapability"

var _ services.Service = &service{}
var _ server.ClientCapability = &service{}

type service struct {
	lggr   logger.SugaredLogger
	client common.OutboundRequestClient
	cfg    common.ServiceConfig
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

	var serviceConfig *common.ServiceConfig
	err := json.Unmarshal([]byte(config), &serviceConfig)
	if err != nil {
		return err
	}
	serviceConfig, err = ApplyDefaultsAndValidate(serviceConfig)
	if err != nil {
		return err
	}
	s.cfg = *serviceConfig

	outboundRequestClient, err := NewOutboundRequestClient(gc, s.cfg, s.lggr)
	if err != nil {
		return err
	}
	s.client = outboundRequestClient

	err = s.Start(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (s *service) Start(ctx context.Context) error {
	s.lggr.Debug("Service starting...")
	err := s.client.Start(ctx)
	if err != nil {
		return err
	}

	s.lggr.Info("Service started")
	return nil
}

func (s *service) Close() error {
	s.lggr.Debug("Service closing...")
	err := s.client.Close()
	if err != nil {
		return err
	}
	s.lggr.Info("Service closed")
	return nil
}

func (s *service) HealthReport() map[string]error {
	return map[string]error{s.Name(): nil}
}

func (s *service) Ready() error {
	return nil
}

func (s *service) Name() string {
	return ServiceName
}

func (s *service) Description() string {
	return "HTTP Actions Service"
}

func (s *service) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *http.Request) (*http.Response, error) {
	s.lggr.Debugf("Received request with metadata: %v", metadata)
	validatedInput, err := ValidatedRequest(input, s.cfg)
	if err != nil {
		s.lggr.Errorf("Failed to validate input: %v", err)
		return nil, err
	}
	return s.client.SendRequest(ctx, metadata, validatedInput)
}

// NewOutboundRequestClient creates an OutboundProxy based on the ServiceConfig.ProxyMode
func NewOutboundRequestClient(gatewayConnector core.GatewayConnector, serviceConfig common.ServiceConfig, lggr logger.Logger) (common.OutboundRequestClient, error) {
	switch serviceConfig.ProxyMode {
	case "direct":
		return common.NewHTTPClientProxy(serviceConfig, lggr)
	case "gateway":
		return gateway.NewGatewayOutboundProxy(gatewayConnector, serviceConfig, lggr)
	default:
		return nil, errors.New("invalid ProxyMode: " + serviceConfig.ProxyMode)
	}
}
