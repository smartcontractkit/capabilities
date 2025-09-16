package action

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/smartcontractkit/capabilities/http_action/common"
	"github.com/smartcontractkit/capabilities/http_action/gateway"
	"github.com/smartcontractkit/capabilities/http_action/validate"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http/server"

	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
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
	rateLimiter   limits.RateLimiter
	validator     *validate.Validator
}

func NewService(lggr logger.Logger, limitsFactory limits.Factory) *service {
	return &service{
		lggr:          logger.Sugared(logger.Named(lggr, ServiceName)),
		limitsFactory: limitsFactory,
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

	err := json.Unmarshal([]byte(config), &s.cfg)
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

	s.client, err = NewOutboundRequestClient(gc, s.cfg, s.lggr, s.metrics, s.validator)
	if err != nil {
		return err
	}

	s.rateLimiter, err = s.limitsFactory.MakeRateLimiter(cresettings.Default.PerWorkflow.HTTPAction.RateLimit)
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
	return ServiceName
}

func (s *service) Description() string {
	return "HTTP Actions Service"
}

func (s *service) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *http.Request) (*capabilities.ResponseAndMetadata[*http.Response], error) {
	s.lggr.Debugf("Received request with metadata: %v", metadata)
	startTime := time.Now()
	s.metrics.IncrementRequestCount(ctx, s.lggr)
	// set the context with the workflow owner and workflow id
	// these are required for request/response/rate limit checks
	ctx = contexts.WithCRE(ctx, contexts.CRE{Owner: metadata.WorkflowOwner, Workflow: metadata.WorkflowID})

	if err := s.CheckRateLimit(ctx, metadata); err != nil {
		return nil, err
	}

	validatedInput, err := s.validator.ValidatedRequest(ctx, input)
	if err != nil {
		s.lggr.Errorf("Failed to validate input: %v", err)
		s.metrics.IncrementInputValidationFailures(ctx, s.lggr)
		return nil, err
	}

	response, err := s.client.SendRequest(ctx, metadata, validatedInput, startTime)
	if err != nil {
		return nil, err
	}

	s.metrics.IncrementSuccessfulResponse(ctx, s.cfg.ProxyMode, response.StatusCode, s.lggr)

	responseAndMetadata := capabilities.ResponseAndMetadata[*http.Response]{
		Response:         response,
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
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

func (s *service) CheckRateLimit(ctx context.Context, metadata capabilities.RequestMetadata) error {
	if err := s.rateLimiter.AllowErr(ctx); err != nil {
		var rl limits.ErrorRateLimited
		if errors.As(err, &rl) {
			if rl.Scope == settings.ScopeWorkflow {
				s.metrics.IncrementWorkflowThrottled(ctx, s.lggr)
			} else {
				s.lggr.Errorf("failed to start execution: unexpected rate limit for scope %s", rl.Scope)
			}
		}
		return err
	}
	return nil
}
