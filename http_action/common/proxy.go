package common

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http" // aliased below to avoid conflict
	"time"

	"github.com/doyensec/safeurl"
	"github.com/google/uuid"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	httpcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

var _ OutboundRequestClient = &httpClientProxy{}

const ClientName = "HTTPClientProxy"

type OutboundRequestClient interface {
	SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *httpcap.Request, startTime time.Time) (*httpcap.Response, error)
	services.Service
}

// ResponseValidator is an interface for validating HTTP responses
type ResponseValidator interface {
	ValidateResponseSize(ctx context.Context, response []byte) error
}

// httpClientProxy implements OutboundRequestClient using a regular HTTP client
type httpClientProxy struct {
	client    *safeurl.WrappedClient
	cfg       ServiceConfig
	lggr      logger.Logger
	validator ResponseValidator
	metrics   *Metrics
}

func disableRedirects(req *http.Request, via []*http.Request) error {
	return errors.New("redirects are not allowed")
}

func NewHTTPClientProxy(cfg ServiceConfig, lggr logger.Logger, validator ResponseValidator, metrics *Metrics) (*httpClientProxy, error) {
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

func headers(req *httpcap.Request) map[string][]string {
	headers := make(map[string][]string)
	for k, v := range req.Headers {
		headers[k] = []string{v}
	}
	return headers
}

func (h *httpClientProxy) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *httpcap.Request, startTime time.Time) (*httpcap.Response, error) {
	ctx = metadata.ContextWithCRE(ctx)
	requestID := uuid.New().String()
	lggr := logger.With(h.lggr, "requestID", requestID, "workflowID", metadata.WorkflowID, "workflowExecutionID", metadata.WorkflowExecutionID, "workflowOwner", metadata.WorkflowOwner)

	timeoutCtx, cancel := context.WithTimeout(ctx, input.Timeout.AsDuration())
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, input.Method, input.Url, bytes.NewReader(input.Body))
	if err != nil {
		h.metrics.IncrementExecutionError(ctx, ProxyModeDirect, lggr)
		return nil, err
	}

	req.Header = http.Header(headers(input))

	lggr.Debugw("Sending HTTP request")
	externalStartTime := time.Now()
	resp, err := h.client.Do(req)
	if err != nil {
		h.metrics.IncrementExternalEndpointError(ctx, ProxyModeDirect, lggr)
		return nil, err
	}
	defer resp.Body.Close()
	externalLatency := time.Since(externalStartTime).Milliseconds()
	lggr.Debugw("Received HTTP response", "status", resp.Status, "statusCode", resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.metrics.IncrementExternalEndpointError(ctx, ProxyModeDirect, lggr)
		return nil, err
	}

	if err := h.validator.ValidateResponseSize(ctx, body); err != nil {
		h.metrics.IncrementExternalEndpointError(ctx, ProxyModeDirect, lggr)
		return nil, err
	}

	headers := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) == 0 {
			continue
		}
		headers[k] = v[0]
	}

	outputs := &httpcap.Response{
		StatusCode: uint32(resp.StatusCode), //nolint:gosec // G115
		Headers:    headers,
		Body:       body,
	}
	totalLatency := time.Since(startTime).Milliseconds()
	h.metrics.RecordRequestLatency(ctx, totalLatency, externalLatency, ProxyModeDirect, lggr)

	return outputs, nil
}

func (h *httpClientProxy) Start(ctx context.Context) error {
	return nil
}

func (h *httpClientProxy) Close() error {
	return nil
}

func (h *httpClientProxy) HealthReport() map[string]error {
	return map[string]error{ClientName: nil}
}

func (h *httpClientProxy) Name() string {
	return ClientName
}

func (h *httpClientProxy) Ready() error {
	return nil
}
