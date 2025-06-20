package monitoring

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

type processor struct {
	metrics *Metrics
	emitter beholder.ProtoEmitter
}

func NewProcessor(emitter beholder.ProtoEmitter) (beholder.ProtoProcessor, error) {
	metrics, err := NewMetrics()
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics: %w", err)
	}
	return &processor{
		emitter: emitter,
		metrics: metrics,
	}, nil
}
func (p *processor) Process(ctx context.Context, m proto.Message, _ ...any) error {
	// Switch on the type of the proto.Message
	switch msg := m.(type) {
	case *CallContractSuccess:
		if err := p.metrics.OnCallContractSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish write initiated metrics: %w", err)
		}
		// TODO emit structured logs
	case *CallContractError:
		if err := p.metrics.OnCallContractError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish write error metrics: %w", err)
		}
	default:
		return nil // fallthrough
	}

	return nil
}
