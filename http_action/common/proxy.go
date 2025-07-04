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

	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"

	"github.com/smartcontractkit/capabilities/http_action/pb"
)

var _ OutboundRequestClient = &httpClientProxy{}

const ClientName = "HTTPClientProxy"

var (
	defaultAllowedPorts   = []int{80, 443}
	defaultAllowedSchemes = []string{"http", "https"}
)

type OutboundRequestClient interface {
	SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *pb.Request) (*pb.Response, error)
	services.Service
}

// httpClientProxy implements OutboundRequestClient using a regular HTTP client
type httpClientProxy struct {
	client              *safeurl.WrappedClient
	cfg                 ServiceConfig
	outgoingRateLimiter *ratelimit.RateLimiter
	lggr                logger.Logger
}

func disableRedirects(req *http.Request, via []*http.Request) error {
	return errors.New("redirects are not allowed")
}

func NewHTTPClientProxy(cfg ServiceConfig, lggr logger.Logger) (*httpClientProxy, error) {
	outgoingRateLimiter, err := ratelimit.NewRateLimiter(cfg.OutgoingRateLimiter)
	if err != nil {
		return nil, err
	}

	clientCfg := ApplyDefaults(&cfg.HTTPClientConfig)
	safeConfig := safeurl.
		GetConfigBuilder().
		SetAllowedIPs(clientCfg.AllowedIPs...).
		SetAllowedIPsCIDR(clientCfg.AllowedIPsCIDR...).
		SetAllowedPorts(clientCfg.AllowedPorts...).
		SetAllowedSchemes(clientCfg.AllowedSchemes...).
		SetBlockedIPs(clientCfg.BlockedIPs...).
		SetBlockedIPsCIDR(clientCfg.BlockedIPsCIDR...).
		SetCheckRedirect(disableRedirects).
		Build()

	return &httpClientProxy{
		cfg:                 cfg,
		client:              safeurl.Client(safeConfig),
		outgoingRateLimiter: outgoingRateLimiter,
		lggr:                lggr,
	}, nil
}

func headers(req *pb.Request) map[string][]string {
	headers := make(map[string][]string)
	for k, v := range req.Headers {
		headers[k] = []string{v}
	}
	return headers
}

func (h *httpClientProxy) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *pb.Request) (*pb.Response, error) {
	requestID := uuid.New().String()
	lggr := logger.With(h.lggr, "requestID", requestID, "workflowID", metadata.WorkflowID, "workflowExecutionID", metadata.WorkflowExecutionID, "workflowOwner", metadata.WorkflowOwner)

	workflowAllow, globalAllow := h.outgoingRateLimiter.AllowVerbose(metadata.WorkflowOwner)
	if !workflowAllow {
		return nil, errors.New(ErrorOutgoingRatelimitWorkflowOwner)
	}
	if !globalAllow {
		return nil, errors.New(ErrorOutgoingRatelimitGlobal)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(input.TimeoutMs)*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, input.Method, input.Url, bytes.NewReader(input.Body))
	if err != nil {
		return nil, err
	}

	req.Header = http.Header(headers(input))

	lggr.Debugw("Sending HTTP request")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	lggr.Debugw("Received HTTP response", "status", resp.Status, "statusCode", resp.StatusCode)
	limited := io.LimitReader(resp.Body, int64(h.cfg.LimitsConfig.MaxResponseBytes))
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}

	headers := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) == 0 {
			continue
		}
		headers[k] = v[0]
	}

	outputs := &pb.Response{
		StatusCode: uint32(resp.StatusCode), //nolint:gosec // G115
		Headers:    headers,
		Body:       body,
	}
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

func ApplyDefaults(c *HTTPClientConfig) *HTTPClientConfig {
	if len(c.AllowedPorts) == 0 {
		c.AllowedPorts = defaultAllowedPorts
	}

	if len(c.AllowedSchemes) == 0 {
		c.AllowedSchemes = defaultAllowedSchemes
	}

	// safeurl automatically blocks internal IPs so no need
	// to set defaults here.
	return c
}
