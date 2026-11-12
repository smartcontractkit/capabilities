// Package pricefeed is a minimal stand-in for the SDK client cre-sdk-go would generate from the price feed
// capability's production protos. It calls a real price feed capability in-process.
package pricefeed

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/smartcontractkit/capabilities/pricing/pricefeed/protos"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/workflow/fakesdk/cre"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

const CapabilityID = protos.ClientID

type (
	GetPriceRequest  = protos.GetPriceRequest
	GetPriceResponse = protos.GetPriceResponse
)

// Capability is what the runtime gives for CapabilityID: the capability, or a client of it.
type Capability interface {
	GetPrice(ctx context.Context, metadata capabilities.RequestMetadata, input *GetPriceRequest) (*capabilities.ResponseAndMetadata[*GetPriceResponse], error)
}

type Client struct{}

// NodeRuntime is what a node-mode callback calls the capability with.
type NodeRuntime struct {
	runtime cre.Runtime
}

// GetPrice returns the current price of the ticker the request names, as this node fetched it.
func (c *Client) GetPrice(runtime *NodeRuntime, req *GetPriceRequest) cre.Promise[*GetPriceResponse] {
	return cre.NewPromise(func() (*GetPriceResponse, error) {
		capability, ok := runtime.runtime.Capability(CapabilityID).(Capability)
		if !ok {
			return nil, fmt.Errorf("capability %s not registered", CapabilityID)
		}

		out, err := capability.GetPrice(runtime.runtime.Context(), runtime.runtime.Metadata(), req)
		if err != nil {
			return nil, err
		}

		return out.Response, nil
	})
}

// RunInNodeMode runs fn on every node and aggregates the results.
func RunInNodeMode[C, T any](
	config C,
	runtime cre.Runtime,
	fn func(C, *slog.Logger, *NodeRuntime) (T, error),
	aggregation cre.ConsensusAggregation[T],
) cre.Promise[T] {
	return cre.NewPromise(func() (T, error) {
		observations := make([]T, 0, runtime.NumNodes())
		for i := range runtime.NumNodes() {
			obs, err := fn(config, runtime.Logger().With("node", i), &NodeRuntime{runtime: runtime})
			if err != nil {
				var zero T
				return zero, fmt.Errorf("node %d: %w", i, err)
			}

			observations = append(observations, obs)
		}

		return aggregation.Aggregate(observations)
	})
}
