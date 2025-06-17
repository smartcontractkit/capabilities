package common

import (
	"bytes"
	"context"
	"io"
	"net/http" // aliased below to avoid conflict
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	httpactions "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
)

var _ OutboundRequestClient = &httpClientProxy{}

type OutboundRequestClient interface {
	SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *httpactions.Request) (*httpactions.Response, error)
	Start(ctx context.Context) error
	Close() error
}

// httpClientProxy implements OutboundRequestClient using a regular HTTP client
// TODO: This client is experimental for now. Add additional protections/configuration. For instance,
// - Server-Side Request Forgery (SSRF): Block internal, private or otherwise restricted IP ranges. Only alow HTTPS
// - Timeouts and Limits (e.g., maximum response size, request timeout ms)
type httpClientProxy struct {
	client                *http.Client
	maxResponseBodyLength uint32
}

func NewHTTPClientProxy(cfg ServiceConfig) *httpClientProxy {
	return &httpClientProxy{
		client: &http.Client{
			Timeout: time.Duration(cfg.LimitsConfig.MaxTimeoutMs) * time.Millisecond,
		},
		maxResponseBodyLength: cfg.LimitsConfig.MaxBodyLength,
	}
}

func headers(req *httpactions.Request) map[string][]string {
	headers := make(map[string][]string)
	for k, v := range req.Headers {
		var values []string
		values = append(values, v)
		headers[k] = values
	}
	return headers
}

func (h *httpClientProxy) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *httpactions.Request) (*httpactions.Response, error) {
	req, err := http.NewRequestWithContext(ctx, input.Method, input.Url, bytes.NewReader(input.Body))
	if err != nil {
		return nil, err
	}

	req.Header = http.Header(headers(input))

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, int64(h.maxResponseBodyLength))
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

	outputs := &httpactions.Response{
		StatusCode: uint32(resp.StatusCode), //nolint:gosec // G115
		Headers:    headers,
		Body:       body,
	}
	return outputs, nil
}

func (h *httpClientProxy) Start(ctx context.Context) error {
	// No-op for direct HTTP client
	return nil
}

func (h *httpClientProxy) Close() error {
	// No-op for direct HTTP client
	return nil
}
