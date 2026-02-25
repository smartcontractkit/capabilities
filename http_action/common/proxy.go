package common

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/doyensec/safeurl"
	"github.com/google/uuid"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	httpcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

var _ OutboundRequestClient = &httpClientProxy{}

const (
	ClientName    = "HTTPClientProxy"
	internalError = "internal error"
)

type OutboundRequestClient interface {
	SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *httpcap.Request, startTime time.Time) (*httpcap.Response, time.Duration, error)
	services.Service
}

// ResponseValidator is an interface for validating HTTP responses
type ResponseValidator interface {
	ValidateResponseSize(ctx context.Context, response []byte) error
}

// RequestValidator validates HTTP requests and responses. Implemented by validate.Validator.
// Clients should call ValidatedRequest at the send boundary so validation runs in one place.
type RequestValidator interface {
	ResponseValidator
	ValidatedRequest(ctx context.Context, input *httpcap.Request) (*httpcap.Request, error)
}

// InputValidationError wraps an error from request validation so the action can map it to a user-facing error.
type InputValidationError struct{ Err error }

func (e InputValidationError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "input validation failed"
}

func (e InputValidationError) Unwrap() error { return e.Err }

// httpClientProxy implements OutboundRequestClient using a regular HTTP client
type httpClientProxy struct {
	client    *safeurl.WrappedClient
	cfg       ServiceConfig
	lggr      logger.Logger
	validator RequestValidator
	metrics   *Metrics
}

var errRedirectsDisabled = errors.New("redirects are not allowed")

func disableRedirects(*http.Request, []*http.Request) error {
	return errRedirectsDisabled
}

func NewHTTPClientProxy(cfg ServiceConfig, lggr logger.Logger, validator RequestValidator, metrics *Metrics) (*httpClientProxy, error) {
	safeConfig := safeurl.
		GetConfigBuilder().
		SetAllowedIPs(cfg.HTTPClientConfig.AllowedIPs...).
		SetAllowedIPsCIDR(cfg.HTTPClientConfig.AllowedIPsCIDR...).
		SetAllowedPorts(cfg.HTTPClientConfig.AllowedPorts...).
		SetAllowedSchemes(cfg.HTTPClientConfig.AllowedSchemes...).
		SetBlockedIPs(cfg.HTTPClientConfig.BlockedIPs...).
		SetBlockedIPsCIDR(cfg.HTTPClientConfig.BlockedIPsCIDR...).
		SetCheckRedirect(disableRedirects).
		Build()

	return &httpClientProxy{
		cfg:       cfg,
		client:    safeurl.Client(safeConfig),
		lggr:      lggr,
		validator: validator,
		metrics:   metrics,
	}, nil
}

func toRequestHeaders(req *httpcap.Request) http.Header {
	h := make(http.Header)
	if len(req.MultiHeaders) > 0 {
		for k, v := range req.MultiHeaders {
			h[k] = slices.Clone(v.GetValues())
		}
		return h
	}
	// TODO: Remove fallback to using Headers.
	for k, v := range req.Headers { //nolint:staticcheck
		h[k] = []string{v}
	}
	return h
}

// toResponseHeaders converts net/http response headers into capability Response format
func toResponseHeaders(header http.Header) (map[string]*httpcap.HeaderValues, map[string]string) {
	multiHeaders := make(map[string]*httpcap.HeaderValues, len(header))
	headers := make(map[string]string, len(header))
	for k, v := range header {
		if len(v) == 0 {
			continue
		}
		multiHeaders[k] = &httpcap.HeaderValues{Values: slices.Clone(v)}
		headers[k] = strings.Join(v, ",") // Join via "," for backwards compatibility.
	}
	return multiHeaders, headers
}

func (h *httpClientProxy) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *httpcap.Request, startTime time.Time) (*httpcap.Response, time.Duration, error) {
	ctx = metadata.ContextWithCRE(ctx)
	requestID := uuid.New().String()
	lggr := logger.With(h.lggr, "requestID", requestID, "workflowID", metadata.WorkflowID, "workflowExecutionID", metadata.WorkflowExecutionID, "workflowOwner", metadata.WorkflowOwner)

	input, err := h.validator.ValidatedRequest(ctx, input)
	if err != nil {
		h.metrics.IncrementInputValidationFailures(ctx, lggr)
		return nil, 0, InputValidationError{Err: err}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, input.Timeout.AsDuration())
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, input.Method, input.Url, bytes.NewReader(input.Body))
	if err != nil {
		h.metrics.IncrementExecutionError(ctx, ProxyModeDirect, lggr)
		lggr.Errorf("failed to create request: %v", err)
		return nil, 0, errors.New(internalError)
	}

	req.Header = toRequestHeaders(input)

	lggr.Debugw("Sending HTTP request")
	externalStartTime := time.Now()
	resp, err := h.client.Do(req)
	if err != nil {
		h.metrics.IncrementExternalEndpointError(ctx, ProxyModeDirect, lggr)
		return nil, 0, err
	}
	defer resp.Body.Close()
	externalLatency := time.Since(externalStartTime)
	lggr.Debugw("Received HTTP response", "status", resp.Status, "statusCode", resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.metrics.IncrementExternalEndpointError(ctx, ProxyModeDirect, lggr)
		return nil, 0, err
	}

	if err := h.validator.ValidateResponseSize(ctx, body); err != nil {
		h.metrics.IncrementExternalEndpointError(ctx, ProxyModeDirect, lggr)
		return nil, 0, err
	}

	multiHeaders, headers := toResponseHeaders(resp.Header)

	outputs := &httpcap.Response{
		StatusCode:   uint32(resp.StatusCode), //nolint:gosec // G115
		Headers:      headers,
		MultiHeaders: multiHeaders,
		Body:         body,
	}

	return outputs, externalLatency, nil
}

func (h *httpClientProxy) Start(ctx context.Context) error {
	return nil
}

func (h *httpClientProxy) Close() error {
	return nil
}

func (h *httpClientProxy) HealthReport() map[string]error {
	return map[string]error{h.Name(): nil}
}

func (h *httpClientProxy) Name() string {
	return h.lggr.Name()
}

func (h *httpClientProxy) Ready() error {
	return nil
}
