package monitoring

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
)

// processor dispatches telemetry messages to metrics and logs for Stellar operations.
type processor struct {
	metrics Metrics
	lggr    logger.Logger
}

func NewProcessor(lggr logger.Logger, metrics Metrics) (beholder.ProtoProcessor, error) {
	return &processor{
		lggr:    lggr,
		metrics: metrics,
	}, nil
}

func (p *processor) Process(ctx context.Context, m proto.Message, attrKVs ...any) error {
	switch msg := m.(type) {
	case *ReadContractInitiated:
		p.logMessage(msg)
	case *ReadContractSuccess:
		p.logMessage(msg)
		if err := p.metrics.OnReadContractSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish ReadContractSuccess metrics: %w", err)
		}
	case *ReadContractError:
		p.logMessage(msg)
		// User errors are caller mistakes, not capability/infra failures, so they are not
		// counted in the error metric (mirrors EVM/Aptos).
		if !msg.GetIsUserError() {
			if err := p.metrics.OnReadContractError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish ReadContractError metrics: %w", err)
			}
		}
	default:
		return nil
	}
	return nil
}

func (p *processor) logMessage(msg proto.Message) {
	mStr := protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}.Format(msg)

	var asMap map[string]any
	if err := json.Unmarshal([]byte(mStr), &asMap); err != nil {
		p.lggr.Errorw("Failed to unmarshal telemetry message for logging",
			"err", err,
			"message_type", msg.ProtoReflect().Descriptor().Name(),
			"json_message", mStr)
		return
	}

	p.lggr.Infow("[Stellar Monitoring]", "message", asMap, "entity_name", msg.ProtoReflect().Descriptor().Name())
}

var LogAndEmitSuccess = commonmon.LogAndEmitSuccess
var EmitInitiated = commonmon.EmitInitiated
var LogAndEmitError = commonmon.LogAndEmitError
