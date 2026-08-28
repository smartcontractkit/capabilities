package common

import (
	"slices"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	gateway "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"

	httpcap "github.com/smartcontractkit/capabilities/http/protos"
)

// OutboundRequest is the workflow's request as something that can be sent
// anywhere: no protos, no capability framing, just what is being asked of the
// internet and on whose behalf.
func OutboundRequest(metadata capabilities.RequestMetadata, input *httpcap.Request) gateway.OutboundHTTPRequest {
	// One of Headers or MultiHeaders, never both: validation has already refused a
	// request that set the two of them.
	var headers map[string]string
	var multiHeaders map[string][]string
	if len(input.MultiHeaders) > 0 {
		multiHeaders = make(map[string][]string, len(input.MultiHeaders))
		for name, values := range input.MultiHeaders {
			multiHeaders[name] = slices.Clone(values.GetValues())
		}
	} else {
		headers = input.Headers //nolint:staticcheck // Headers is deprecated but still set when MultiHeaders is not
	}

	var mtls *gateway.MtlsAuth
	if input.Mtls != nil {
		mtls = &gateway.MtlsAuth{
			PrivateKey:  gateway.Secret(input.Mtls.PrivateKey),
			Certificate: input.Mtls.Certificate,
		}
	}

	return gateway.OutboundHTTPRequest{
		WorkflowID:    metadata.WorkflowID,
		WorkflowOwner: metadata.WorkflowOwner,
		URL:           input.Url,
		Method:        input.Method,
		Headers:       headers,
		MultiHeaders:  multiHeaders,
		Body:          input.Body,
		// The casts are safe: validation has bounded both.
		TimeoutMs: uint32(input.Timeout.AsDuration().Milliseconds()), //nolint:gosec // G115
		CacheSettings: gateway.CacheSettings{
			Store:    input.CacheSettings.Store,
			MaxAgeMs: int32(input.CacheSettings.MaxAge.AsDuration().Milliseconds()), //nolint:gosec // G115
		},
		Mtls: mtls,
	}
}

// ResponseHeaders is an answer's headers as a workflow reads them. Both forms are
// filled in, one from the other, because a workflow may read either.
func ResponseHeaders(response *gateway.OutboundHTTPResponse) (map[string]string, map[string]*httpcap.HeaderValues) {
	if len(response.MultiHeaders) > 0 {
		multi := make(map[string]*httpcap.HeaderValues, len(response.MultiHeaders))
		headers := make(map[string]string, len(response.MultiHeaders))
		for name, values := range response.MultiHeaders {
			key := SanitizeUTF8(name)
			sanitized := make([]string, len(values))
			for i, value := range values {
				sanitized[i] = SanitizeUTF8(value)
			}
			multi[key] = &httpcap.HeaderValues{Values: sanitized}
			headers[key] = strings.Join(sanitized, ",") // Joined with "," for backwards compatibility.
		}
		return headers, multi
	}

	multi := make(map[string]*httpcap.HeaderValues, len(response.Headers))
	headers := make(map[string]string, len(response.Headers))
	for name, value := range response.Headers {
		key, sanitized := SanitizeUTF8(name), SanitizeUTF8(value)
		headers[key] = sanitized
		multi[key] = &httpcap.HeaderValues{Values: []string{sanitized}}
	}
	return headers, multi
}
