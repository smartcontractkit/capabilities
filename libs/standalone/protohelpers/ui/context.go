package ui

import (
	"context"
	"net/http"

	"google.golang.org/grpc/metadata"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// metadataFromContext reads the request metadata off the gRPC metadata the form
// generator forwarded from the browser's headers.
//
// The headers reach here as outgoing metadata, keyed lowercase, which is why the
// lookup lowercases rather than relying on the caller's casing.
func metadataFromContext(ctx context.Context) (capabilities.RequestMetadata, error) {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.MD{}
	}
	return MetadataFromHeaders(func(name string) []string {
		return md.Get(name)
	})
}

// MetadataFromRequest is the same for an ordinary HTTP request, which is what the
// fan-out endpoint has: it holds headers rather than gRPC metadata.
func MetadataFromRequest(r *http.Request) (capabilities.RequestMetadata, error) {
	return MetadataFromHeaders(func(name string) []string {
		return r.Header.Values(name)
	})
}

// triggerIDFromContext is the trigger ID a registration named, or a fresh one.
func triggerIDFromContext(ctx context.Context) string {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.MD{}
	}
	return TriggerIDFromHeaders(md.Get)
}

// HeaderNames is every header the metadata travels in, which is what the form
// generator has to be told to forward.
func HeaderNames() []string {
	fields := Fields()
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Header)
	}
	return names
}

// PreservedHeaders is every header a request may carry that has to reach Invoke:
// the metadata, and the trigger ID a subscription is identified by.
//
// The form generator drops anything it was not told to keep, so a header missing
// from here is one the page can offer a box for and never send.
func PreservedHeaders() []string {
	return append(HeaderNames(), TriggerIDHeader)
}
