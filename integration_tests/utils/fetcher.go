package utils

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/custmsg"
	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	wasmpb "github.com/smartcontractkit/chainlink-common/pkg/workflows/wasm/pb"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/compute"
)

type NoopComputeFetcherFactory struct{}

func (n NoopComputeFetcherFactory) NewFetcher(log commonlogger.Logger, emitter custmsg.MessageEmitter) compute.FetcherFn {
	return func(ctx context.Context, req *wasmpb.FetchRequest) (*wasmpb.FetchResponse, error) {
		return nil, fmt.Errorf("no fetcher configured")
	}
}
