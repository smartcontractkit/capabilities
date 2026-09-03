package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/doyensec/safeurl"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	gateway "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"

	httpcap "github.com/smartcontractkit/capabilities/http/protos"
)

const (
	ClientName    = "HTTPClientProxy"
	internalError = "internal error"
)

// ResponseValidator is an interface for validating HTTP responses
type ResponseValidator interface {
	ValidateResponseSize(ctx context.Context, response []byte) error
}

// RequestValidator validates HTTP requests and responses. Implemented by validate.Validator.
// Clients should call ValidatedRequest at the send boundary so validation runs in one place.
type RequestValidator interface {
	ResponseValidator
	ValidatedRequest(ctx context.Context, input *httpcap.Request) (*httpcap.Request, error)
	ResolveGatewayProxyDonID(ctx context.Context) (string, error)
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

// direct makes the request from this process.
//
// It is the Outbound a run with no gateway gets: nothing stands between this and
// the far side, so the protection that a gateway would give has to be here -
// safeurl refuses the addresses, ports and schemes a workflow must not reach.
type direct struct {
	client *safeurl.WrappedClient
	lggr   logger.Logger
}

var _ Outbound = (*direct)(nil)

var errRedirectsDisabled = errors.New("redirects are not allowed")

func disableRedirects(*http.Request, []*http.Request) error {
	return errRedirectsDisabled
}

// NewDirect returns the Outbound that makes requests itself.
func NewDirect(cfg HTTPClientConfig, lggr logger.Logger) (*direct, error) {
	cfg = cfg.WithDefaults()
	safeConfig := safeurl.
		GetConfigBuilder().
		SetAllowedIPs(cfg.AllowedIPs...).
		SetAllowedIPsCIDR(cfg.AllowedIPsCIDR...).
		SetAllowedPorts(cfg.AllowedPorts...).
		SetAllowedSchemes(cfg.AllowedSchemes...).
		SetBlockedIPs(cfg.BlockedIPs...).
		SetBlockedIPsCIDR(cfg.BlockedIPsCIDR...).
		SetCheckRedirect(disableRedirects).
		Build()

	return &direct{client: safeurl.Client(safeConfig), lggr: logger.Named(lggr, "Direct")}, nil
}

func (h *direct) SendRequest(ctx context.Context, request gateway.OutboundHTTPRequest) (gateway.OutboundHTTPResponse, error) {
	timeout := time.Duration(request.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, request.Method, request.URL, bytes.NewReader(request.Body))
	if err != nil {
		h.lggr.Errorf("failed to create request: %v", err)
		return gateway.OutboundHTTPResponse{}, errors.New(internalError)
	}
	req.Header = RequestHeaders(request)

	h.lggr.Debugw("Sending HTTP request", "url", request.URL)
	started := time.Now()
	resp, err := h.client.Do(req)
	if err != nil {
		// safeurl refuses what a workflow must not reach, and the far side refuses what
		// it likes: either way this is the workflow's request failing, not this.
		return gateway.OutboundHTTPResponse{}, NewUserError(err)
	}
	defer resp.Body.Close()
	latency := time.Since(started)

	body, err := io.ReadAll(io.LimitReader(resp.Body, responseLimit(request)))
	if err != nil {
		return gateway.OutboundHTTPResponse{}, NewUserError(err)
	}
	h.lggr.Debugw("Received HTTP response", "status", resp.Status, "statusCode", resp.StatusCode)

	return ResponseOf(resp, body, latency), nil
}

// DefaultRequestTimeout bounds a request whose caller named no timeout.
const DefaultRequestTimeout = 30 * time.Second

// defaultResponseLimit is how much of an answer is read when the caller named no
// limit of its own.
const defaultResponseLimit = 5 * 1024 * 1024

func responseLimit(request gateway.OutboundHTTPRequest) int64 {
	if request.MaxResponseBytes > 0 {
		return int64(request.MaxResponseBytes)
	}
	return defaultResponseLimit
}

// RequestHeaders is an outbound request's headers as net/http wants them.
//
// Shared by every Outbound that reaches the internet with net/http - which is all
// of them that reach it at all, whether directly or through a tunnel.
func RequestHeaders(request gateway.OutboundHTTPRequest) http.Header {
	h := make(http.Header, len(request.MultiHeaders)+len(request.Headers))
	if len(request.MultiHeaders) > 0 {
		for k, v := range request.MultiHeaders {
			h[http.CanonicalHeaderKey(k)] = slices.Clone(v)
		}
		return h
	}
	for k, v := range request.Headers {
		h.Set(k, v)
	}
	return h
}

// ResponseOf is what an Outbound answers with, from what net/http answered.
func ResponseOf(resp *http.Response, body []byte, latency time.Duration) gateway.OutboundHTTPResponse {
	headers := make(map[string]string, len(resp.Header))
	multi := make(map[string][]string, len(resp.Header))
	for key, values := range resp.Header {
		if len(values) == 0 {
			continue
		}
		// HTTP header names and values may hold arbitrary bytes, and these end up in
		// proto strings, which have to be valid UTF-8 to cross gRPC.
		name := SanitizeUTF8(key)
		sanitized := make([]string, len(values))
		for i, value := range values {
			sanitized[i] = SanitizeUTF8(value)
		}
		multi[name] = sanitized
		headers[name] = strings.Join(sanitized, ",") // Joined with "," for backwards compatibility.
	}

	return gateway.OutboundHTTPResponse{
		StatusCode:              resp.StatusCode,
		Headers:                 headers,
		MultiHeaders:            multi,
		Body:                    body,
		ExternalEndpointLatency: latency,
	}
}

// SanitizeUTF8 returns s unchanged if it is already valid UTF-8, otherwise it
// replaces every invalid byte with the Unicode replacement character (U+FFFD).
// HTTP header names/values may contain arbitrary bytes, but proto string fields
// must be valid UTF-8 to marshal over gRPC.
func SanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

func (h *direct) Start(context.Context) error { return nil }

func (h *direct) Close() error { return nil }

func (h *direct) HealthReport() map[string]error {
	return map[string]error{h.Name(): nil}
}

func (h *direct) Name() string {
	return h.lggr.Name()
}

func (h *direct) Ready() error {
	return nil
}

// Gateway is the gateway connection, as the client that goes out through one
// needs it: somewhere to send a request for the gateway to fetch, and somewhere
// to open a connection the gateway will carry without reading.
//
// Both, because they are one gateway on one address, reached by one handshake -
// so a node that has the connection has both, and which of the two a request uses
// is the client's decision to make rather than something to be configured.
type Gateway interface {
	core.GatewayConnector

	// Request asks the gateway for something this node is waiting on - a URL to
	// fetch - and returns what it answered. The answer is the same message the
	// gateway would otherwise have pushed back, so what a caller reads is unchanged;
	// what it no longer has to do is recognise it among everything else arriving.
	Request(ctx context.Context, gatewayID string, msg *jsonrpc.Response[json.RawMessage]) (*jsonrpc.Request[json.RawMessage], error)

	// Tunnel opens a connection to address through gatewayID, for a request the
	// gateway is not to see.
	Tunnel(ctx context.Context, gatewayID, address string) (net.Conn, error)
}
