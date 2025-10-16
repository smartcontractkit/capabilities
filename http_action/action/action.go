package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/smartcontractkit/capabilities/http_action/common"
	"github.com/smartcontractkit/capabilities/http_action/gateway"
	"github.com/smartcontractkit/capabilities/http_action/validate"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http/server"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"

	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const ServiceName = "HTTPActionCapability"

var _ services.Service = &service{}
var _ server.ClientCapability = &service{}

type service struct {
	lggr          logger.SugaredLogger
	client        common.OutboundRequestClient
	cfg           common.ServiceConfig
	metrics       *common.Metrics
	limitsFactory limits.Factory
	validator     *validate.Validator
}

func NewService(lggr logger.Logger, limitsFactory limits.Factory) *service {
	return &service{
		lggr:          logger.Sugared(logger.Named(lggr, ServiceName)),
		limitsFactory: limitsFactory,
	}
}

func (s *service) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	s.lggr.Debugf("Initialising %s. config: %s", ServiceName, dependencies.Config)

	err := json.Unmarshal([]byte(dependencies.Config), &s.cfg)
	if err != nil {
		return err
	}
	s.cfg.ApplyDefault()

	s.metrics, err = common.NewMetrics()
	if err != nil {
		return err
	}

	s.validator, err = validate.NewValidator(s.lggr, s.limitsFactory)
	if err != nil {
		return err
	}

	s.client, err = NewOutboundRequestClient(dependencies.GatewayConnector, s.cfg, s.lggr, s.metrics, s.validator)
	if err != nil {
		return err
	}

	return s.Start(ctx)
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
	return s.lggr.Name()
}

func (s *service) Description() string {
	return "HTTP Actions Service"
}

func (s *service) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *http.Request) (*capabilities.ResponseAndMetadata[*http.Response], error) {
	s.lggr.Debugf("Received request with metadata: %v", metadata)
	ctx = metadata.ContextWithCRE(ctx)
	startTime := time.Now()
	s.metrics.IncrementRequestCount(ctx, s.lggr)
	// set the context with the workflow owner and workflow id
	// these are required for request/response checks
	ctx = metadata.ContextWithCRE(ctx)

	validatedInput, err := s.validator.ValidatedRequest(ctx, input)
	if err != nil {
		s.metrics.IncrementInputValidationFailures(ctx, s.lggr)
		return nil, fmt.Errorf("input validation failed for workflow %s (ID: %s, Owner: %s, ExecutionID: %s): %w",
			metadata.WorkflowName, metadata.WorkflowID, metadata.WorkflowOwner, metadata.WorkflowExecutionID, err)
	}

	response, err := s.client.SendRequest(ctx, metadata, validatedInput, startTime)
	if err != nil {
		return nil, capabilities.NewRemoteReportableError(
			fmt.Errorf("request failed for workflow %s (ID: %s, Owner: %s, ExecutionID: %s): %w",
				metadata.WorkflowName, metadata.WorkflowID, metadata.WorkflowOwner, metadata.WorkflowExecutionID, err))
	}

	s.metrics.IncrementSuccessfulResponse(ctx, s.cfg.ProxyMode, response.StatusCode, s.lggr)

	responseAndMetadata := capabilities.ResponseAndMetadata[*http.Response]{
		Response:         response,
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	s.lggr.Debugf("Processed request for workflow %s (ID: %s, Owner: %s, ExecutionID: %s)",
		metadata.WorkflowName, metadata.WorkflowID, metadata.WorkflowOwner, metadata.WorkflowExecutionID)
	return &responseAndMetadata, err
}

// NewOutboundRequestClient creates an OutboundProxy based on the ServiceConfig.ProxyMode
func NewOutboundRequestClient(gatewayConnector core.GatewayConnector, serviceConfig common.ServiceConfig, lggr logger.Logger, metrics *common.Metrics, validator common.ResponseValidator) (common.OutboundRequestClient, error) {
	switch serviceConfig.ProxyMode {
	case common.ProxyModeDirect:
		return common.NewHTTPClientProxy(serviceConfig, lggr, validator, metrics)
	case common.ProxyModeGateway:
		return gateway.NewGatewayOutboundProxy(gatewayConnector, serviceConfig, lggr, metrics, validator)
	default:
		return nil, errors.New("invalid ProxyMode: " + serviceConfig.ProxyMode.String())
	}
}
