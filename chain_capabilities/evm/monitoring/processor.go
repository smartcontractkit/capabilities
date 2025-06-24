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

func (p *processor) Process(ctx context.Context, m proto.Message, attrKVs ...any) error {
	switch msg := m.(type) {
	case *CallContractInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit CallContractInitiated log: %w", err)
		}
	case *CallContractSuccess:
		if err := p.metrics.OnCallContractSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish CallContractSuccess metrics: %w", err)
		}

		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit CallContractSuccess log: %w", err)
		}
	case *CallContractError:
		if err := p.metrics.OnCallContractError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish CallContractError metrics: %w", err)
		}

		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit CallContractError log: %w", err)
		}
	default:
		return nil
	}
	return nil
}
