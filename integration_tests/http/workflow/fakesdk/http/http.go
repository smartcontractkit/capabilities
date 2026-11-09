// Package http is a minimal stand-in for github.com/smartcontractkit/cre-sdk-go/capabilities/networking/http that
// calls a real HTTP action capability in-process.
package http

import (
	"fmt"
	"log/slog"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http/server"

	"github.com/smartcontractkit/capabilities/integration_tests/http/workflow/fakesdk/cre"
)

const CapabilityID = "http-actions@1.0.0-alpha"

type (
	Request  = http.Request
	Response = http.Response
)

type Client struct{}

type SendRequester struct {
	runtime cre.Runtime
}

func (s *SendRequester) SendRequest(req *Request) cre.Promise[*Response] {
	return cre.NewPromise(func() (*Response, error) {
		capability, ok := s.runtime.Capability(CapabilityID).(server.ClientCapability)
		if !ok {
			return nil, fmt.Errorf("capability %s not registered", CapabilityID)
		}
		out, err := capability.SendRequest(s.runtime.Context(), s.runtime.Metadata(), req)
		if err != nil {
			return nil, err
		}
		return out.Response, nil
	})
}

// SendRequest runs fn on every node and aggregates the results.
func SendRequest[C, T any](
	config C,
	runtime cre.Runtime,
	_ *Client,
	fn func(C, *slog.Logger, *SendRequester) (T, error),
	aggregation cre.ConsensusAggregation[T],
) cre.Promise[T] {
	return cre.NewPromise(func() (T, error) {
		observations := make([]T, 0, runtime.NumNodes())
		for i := range runtime.NumNodes() {
			obs, err := fn(config, runtime.Logger().With("node", i), &SendRequester{runtime: runtime})
			if err != nil {
				var zero T
				return zero, fmt.Errorf("node %d: %w", i, err)
			}
			observations = append(observations, obs)
		}
		return aggregation.Aggregate(observations)
	})
}
