package action

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/smartcontractkit/capabilities/http/common"
	"github.com/smartcontractkit/capabilities/http/validate"

	"github.com/smartcontractkit/capabilities/http/protos"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"

	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

const ServiceName = "HTTPActionCapability"

var (
	_ services.Service        = &service{}
	_ protos.ClientCapability = &service{}
)

type service struct {
	lggr          logger.SugaredLogger
	outbound      common.Outbound
	metrics       *common.Metrics
	limitsFactory limits.Factory
	validator     *validate.Validator
}

// Dependencies are what this capability cannot make for itself.
type Dependencies struct {
	// Outbound is how a request leaves this process, and the only thing here that
	// knows how: whether it goes to a gateway, through a tunnel, or straight out of
	// this process is settled by which one of these is handed over. This capability
	// decides whether a request may be made at all - the limits, the validation, the
	// errors a workflow is answered with - and never what happens to it after that.
	//
	// So a CLI, or an enclave with no gateway to speak of, runs this capability
	// unchanged by supplying its own.
	Outbound common.Outbound

	// LimitsFactory is where this capability's own limits come from.
	LimitsFactory limits.Factory
}

// NewService returns the HTTP action capability, ready to be started.
//
// Everything it needs is an argument: there is no Initialise step, because the
// process hosting this is the binary it lives in rather than a node handing it
// dependencies after the fact.
func NewService(lggr logger.Logger, deps Dependencies) (*service, error) {
	if deps.Outbound == nil {
		return nil, errors.New("the HTTP action capability needs somewhere for its requests to go: see common.Outbound")
	}

	s := &service{
		lggr:          logger.Sugared(logger.Named(lggr, ServiceName)),
		outbound:      deps.Outbound,
		limitsFactory: deps.LimitsFactory,
	}

	var err error
	if s.metrics, err = common.NewMetrics(); err != nil {
		return nil, err
	}
	if s.validator, err = validate.NewValidator(s.lggr, s.limitsFactory); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *service) Start(ctx context.Context) error {
	s.lggr.Debug("Service starting...")
	err := s.outbound.Start(ctx)
	if err != nil {
		return err
	}

	s.lggr.Info("Service started")
	return nil
}

func (s *service) Close() error {
	s.lggr.Debug("Service closing...")
	err := s.outbound.Close()
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

func (s *service) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *protos.Request) (*capabilities.ResponseAndMetadata[*protos.Response], caperrors.Error) {
	s.lggr.Debugw("Received request", "metadata", metadata)

	// The workflow this is on behalf of, so that everything below - the limits, the
	// settings, whatever the Outbound looks up - is resolved for the right one.
	ctx = metadata.ContextWithCRE(ctx)
	startTime := time.Now()
	s.metrics.IncrementRequestCount(ctx, s.lggr)

	// Whether the workflow may make this request at all, in one place: what leaves
	// here is a request the capability has already agreed to.
	validated, err := s.validator.ValidatedRequest(ctx, input)
	if err != nil {
		s.metrics.IncrementInputValidationFailures(ctx, s.lggr)
		return nil, s.failed(ctx, metadata, startTime, 0, common.InputValidationError{Err: err})
	}

	response, latency, err := s.send(ctx, metadata, validated)
	if err != nil {
		s.lggr.Errorw("request failed", "error", err, "workflowID", metadata.WorkflowID, "workflowOwner", metadata.WorkflowOwner, "workflowExecutionID", metadata.WorkflowExecutionID)
		return nil, s.failed(ctx, metadata, startTime, latency, err)
	}

	s.metrics.IncrementSuccessfulResponse(ctx, response.StatusCode, s.lggr)
	s.metrics.RecordRequestLatency(ctx, time.Since(startTime).Milliseconds(), latency.Milliseconds(), true, s.lggr)

	s.lggr.Debugw("Processed HTTP request",
		"workflowName", metadata.DecodedWorkflowName,
		"workflowID", metadata.WorkflowID,
		"workflowOwner", metadata.WorkflowOwner,
		"workflowExecutionID", metadata.WorkflowExecutionID,
		"responseStatusCode", response.StatusCode,
		"responseBodySize", len(response.Body),
		"responseNumHeaders", len(response.MultiHeaders),
		"externalEndpointLatency", latency.Milliseconds())

	return &capabilities.ResponseAndMetadata[*protos.Response]{
		Response:         response,
		ResponseMetadata: capabilities.ResponseMetadata{},
	}, nil
}

// send hands a validated request to whatever makes requests here, and reads the
// answer back into what a workflow is given.
//
// The two conversions are what keeps the Outbound free of this capability's
// protos: what it is given and what it returns is an ordinary outbound HTTP
// request and its answer.
func (s *service) send(ctx context.Context, metadata capabilities.RequestMetadata, input *protos.Request) (*protos.Response, time.Duration, error) {
	answered, err := s.outbound.SendRequest(ctx, common.OutboundRequest(metadata, input))
	if err != nil {
		return nil, answered.ExternalEndpointLatency, err
	}

	headers, multiHeaders := common.ResponseHeaders(&answered)
	response := &protos.Response{
		StatusCode:   uint32(answered.StatusCode), //nolint:gosec // G115 - an HTTP status
		Headers:      headers,
		MultiHeaders: multiHeaders,
		Body:         answered.Body,
	}

	// Checked here rather than by whoever fetched it: how much a workflow may be
	// answered with is this capability's limit, and one limit applied in one place
	// is the same limit however the request went out. Counted as the failed request
	// it becomes, by failed() - not as an endpoint error, which is a fact about the
	// far side rather than about what this allows.
	if err := s.validator.ValidateResponseSize(ctx, response.Body); err != nil {
		return nil, answered.ExternalEndpointLatency, common.NewUserError(err)
	}

	return response, answered.ExternalEndpointLatency, nil
}

// failed is how a workflow is told about a request that did not happen.
//
// What separates the kinds is whose fault it was: what the workflow asked for
// (its URL, its endpoint, its certificate) is its own to fix and is returned as a
// user error, and anything else is this failing and is returned as one.
func (s *service) failed(ctx context.Context, metadata capabilities.RequestMetadata, startTime time.Time, latency time.Duration, err error) caperrors.Error {
	s.metrics.RecordRequestLatency(ctx, time.Since(startTime).Milliseconds(), latency.Milliseconds(), false, s.lggr)

	where := fmt.Sprintf("workflowID %s (Owner: %s, Name: %s, ExecutionID: %s)",
		metadata.WorkflowID, metadata.WorkflowOwner, metadata.WorkflowName, metadata.WorkflowExecutionID)

	var validationErr common.InputValidationError
	if errors.As(err, &validationErr) {
		return caperrors.NewPublicUserError(
			fmt.Errorf("input validation failed for %s: %w", where, err), common.UserErrorCode(validationErr.Err))
	}

	var userErr common.UserError
	if errors.As(err, &userErr) {
		return caperrors.NewPublicUserError(fmt.Errorf("request failed for %s: %w", where, err), common.UserErrorCode(err))
	}

	return caperrors.NewPublicSystemError(fmt.Errorf("request failed for %s: %w", where, err), caperrors.Internal)
}
