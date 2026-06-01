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
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http/server"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"

	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const ServiceName = "HTTPActionCapability"

var (
	_ services.Service        = &service{}
	_ server.ClientCapability = &service{}
)

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
	s.lggr.Debugw("Initialising http action capability", "config", dependencies.Config)

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

func (s *service) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *http.Request) (*capabilities.ResponseAndMetadata[*http.Response], caperrors.Error) {
	s.lggr.Debugw("Received request", "metadata", metadata)
	ctx = metadata.ContextWithCRE(ctx)
	startTime := time.Now()
	s.metrics.IncrementRequestCount(ctx, s.lggr)
	// set the context with the workflow owner and workflow id
	// these are required for request/response checks
	ctx = metadata.ContextWithCRE(ctx)

	// Validation runs in the client (gateway or direct) via ValidatedRequest
	response, externalEndpointLatency, err := s.client.SendRequest(ctx, metadata, input, startTime)
	if err != nil {
		s.lggr.Errorw("request failed", "error", err, "workflowID", metadata.WorkflowID, "workflowOwner", metadata.WorkflowOwner, "workflowExecutionID", metadata.WorkflowExecutionID)
		s.metrics.RecordRequestLatency(ctx, time.Since(startTime).Milliseconds(), externalEndpointLatency.Milliseconds(), s.cfg.ProxyMode, false, s.lggr)
		var validationErr common.InputValidationError
		if errors.As(err, &validationErr) {
			return nil, caperrors.NewPublicUserError(
				fmt.Errorf("input validation failed for workflowID %s (Owner: %s, Name: %s, ExecutionID: %s): %w",
					metadata.WorkflowID, metadata.WorkflowOwner, metadata.WorkflowName, metadata.WorkflowExecutionID, err),
				common.UserErrorCode(validationErr.Err))
		}
		var userErr gateway.UserError
		if errors.As(err, &userErr) {
			return nil, caperrors.NewPublicUserError(
				fmt.Errorf("request failed for workflowID %s (Owner: %s, Name: %s, ExecutionID: %s): %w",
					metadata.WorkflowID, metadata.WorkflowOwner, metadata.WorkflowName, metadata.WorkflowExecutionID, err),
				common.UserErrorCode(err))
		}
		return nil, caperrors.NewPublicSystemError(
			fmt.Errorf("request failed for workflowID %s (Owner: %s, Name: %s, ExecutionID: %s): %w",
				metadata.WorkflowID, metadata.WorkflowOwner, metadata.WorkflowName, metadata.WorkflowExecutionID, err), caperrors.Internal)
	}

	s.metrics.IncrementSuccessfulResponse(ctx, s.cfg.ProxyMode, response.StatusCode, s.lggr)
	s.metrics.RecordRequestLatency(ctx, time.Since(startTime).Milliseconds(), externalEndpointLatency.Milliseconds(), s.cfg.ProxyMode, true, s.lggr)

	responseAndMetadata := capabilities.ResponseAndMetadata[*http.Response]{
		Response:         response,
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	s.lggr.Debugw("Processed HTTP request",
		"workflowName", metadata.DecodedWorkflowName,
		"workflowID", metadata.WorkflowID,
		"workflowOwner", metadata.WorkflowOwner,
		"workflowExecutionID", metadata.WorkflowExecutionID,
		"responseStatusCode", response.StatusCode,
		"responseBodySize", len(response.Body),
		"responseNumHeaders", len(response.MultiHeaders),
		"externalEndpointLatency", externalEndpointLatency.Milliseconds())

	return &responseAndMetadata, nil
}

// NewOutboundRequestClient creates an OutboundProxy based on the ServiceConfig.ProxyMode
func NewOutboundRequestClient(gatewayConnector core.GatewayConnector, serviceConfig common.ServiceConfig, lggr logger.Logger, metrics *common.Metrics, validator common.RequestValidator) (common.OutboundRequestClient, error) {
	switch serviceConfig.ProxyMode {
	case common.ProxyModeDirect:
		return common.NewHTTPClientProxy(serviceConfig, lggr, validator, metrics)
	case common.ProxyModeGateway:
		return gateway.NewGatewayOutboundProxy(gatewayConnector, serviceConfig, lggr, metrics, validator)
	default:
		return nil, errors.New("invalid ProxyMode: " + serviceConfig.ProxyMode.String())
	}
}
