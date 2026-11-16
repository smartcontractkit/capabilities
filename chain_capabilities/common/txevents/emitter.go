package txevents

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/durableemitter"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const (
	// DefaultDomain is the ChIP source domain (and therefore the Kafka topic) the events are
	// published to. Placeholder until Data Platform provisions the dedicated domain (PLEX-3409).
	DefaultDomain = "cre-chain-transactions"

	// Attribute names understood by chainlink-common's durable emitter and chipingress client.
	attrSource         = "source"
	attrType           = "type"
	attrIdempotencyKey = "idempotencykey" // chipingress.IdempotencyKeyAttr
	attrSubject        = "subject"

	// emitTimeout bounds the synchronous durable-emitter insert so a slow node database cannot
	// stall the WriteReport that produced the event.
	emitTimeout = 2 * time.Second
)

// EntityType returns the CloudEvent type, and the schema full name, of ChainTransactionObserved.
// It is a function because the generated proto registers itself in an init function that runs
// after package-level variables are initialised.
func EntityType() string {
	return string(proto.MessageName(&ChainTransactionObserved{}))
}

// ErrEmitterUnavailable is returned when the process has no durable emitter, which is the case
// when the node runs without CL_CHIP_INGRESS_DURABLE_EMITTER_ENABLED. Events are dropped and
// counted.
var ErrEmitterUnavailable = errors.New("durable emitter not available in this process")

// Sink delivers an encoded event with its routing attributes. Production uses GlobalSink; tests
// substitute a fake.
type Sink interface {
	Emit(ctx context.Context, body []byte, attrKVs ...any) error
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(ctx context.Context, body []byte, attrKVs ...any) error

// Emit implements Sink.
func (f SinkFunc) Emit(ctx context.Context, body []byte, attrKVs ...any) error {
	return f(ctx, body, attrKVs...)
}

// GlobalSink delivers events through the process-global durable emitter, the same path the
// platform uses for its own ChIP events (workflow trigger events, relayer config). In a LOOP
// that emitter is set up by loop.Server when the node runs with
// CL_CHIP_INGRESS_DURABLE_EMITTER_ENABLED; it persists events in the node database and the host
// delivers them to ChIP Ingress at least once. The emitter is resolved on every call, so the
// sink can be built before the LOOP has finished starting. Without a durable emitter it returns
// ErrEmitterUnavailable.
func GlobalSink() Sink {
	return SinkFunc(func(ctx context.Context, body []byte, attrKVs ...any) error {
		err := durableemitter.GlobalEmit(ctx, body, attrKVs...)
		if errors.Is(err, durableemitter.ErrNotInitialized) {
			return ErrEmitterUnavailable
		}
		return err
	})
}

// Emitter publishes ChainTransactionObserved events.
type Emitter interface {
	Emit(ctx context.Context, event *ChainTransactionObserved) error
}

// NopEmitter discards events. Used when transaction events are disabled.
type NopEmitter struct{}

// Emit implements Emitter.
func (NopEmitter) Emit(context.Context, *ChainTransactionObserved) error { return nil }

// Key identifies the transaction independently of the node that observed it. Every node that
// saw the same transaction produces the same key, so ChIP and the Blockchain Finality Service
// can dedup on it. It is used both as the CloudEvent subject and as the idempotency key.
func Key(ev *ChainTransactionObserved) string {
	return fmt.Sprintf("%d:%s", ev.GetChainSelector(), ev.GetTxHash())
}

// LogKVs returns the event's identifying fields as logger key/values.
func LogKVs(ev *ChainTransactionObserved) []any {
	return []any{
		"txHash", ev.GetTxHash(),
		"chainSelector", ev.GetChainSelector(),
		"txStatus", ev.GetTxStatus().String(),
		"workflowExecutionID", ev.GetWorkflowExecutionId(),
		"workflowID", ev.GetWorkflowId(),
	}
}

// DurableEmitter encodes events as ChIP CloudEvents and hands them to a Sink, counting the
// outcome.
type DurableEmitter struct {
	lggr    logger.Logger
	domain  string
	sink    Sink
	metrics *emitterMetrics

	unavailableLogged atomic.Bool
}

// NewDurableEmitter returns an emitter publishing to the given ChIP source domain through sink.
func NewDurableEmitter(lggr logger.Logger, domain string, sink Sink) (*DurableEmitter, error) {
	if sink == nil {
		return nil, errors.New("nil tx events sink")
	}
	if domain == "" {
		domain = DefaultDomain
	}
	m, err := newEmitterMetrics()
	if err != nil {
		return nil, err
	}
	return &DurableEmitter{
		lggr:    logger.Named(lggr, "TxEvents"),
		domain:  domain,
		sink:    sink,
		metrics: m,
	}, nil
}

// Emit implements Emitter. When the sink reports ErrEmitterUnavailable the event is counted as
// dropped and the condition is logged once per process.
func (d *DurableEmitter) Emit(ctx context.Context, ev *ChainTransactionObserved) error {
	if ev == nil {
		return errors.New("nil ChainTransactionObserved")
	}
	body, err := proto.Marshal(ev)
	if err != nil {
		d.metrics.recordError(ctx, ev, "marshal")
		return fmt.Errorf("marshal ChainTransactionObserved: %w", err)
	}

	emitCtx, cancel := context.WithTimeout(ctx, emitTimeout)
	defer cancel()
	key := Key(ev)
	err = d.sink.Emit(emitCtx, body,
		attrSource, d.domain,
		attrType, EntityType(),
		attrIdempotencyKey, key,
		attrSubject, key,
	)
	switch {
	case errors.Is(err, ErrEmitterUnavailable):
		d.metrics.recordError(ctx, ev, "unavailable")
		if d.unavailableLogged.CompareAndSwap(false, true) {
			d.lggr.Warnw("Transaction events are enabled but this process has no durable emitter; events are dropped", "domain", d.domain)
		}
		return ErrEmitterUnavailable
	case err != nil:
		d.metrics.recordError(ctx, ev, "emit")
		return fmt.Errorf("emit ChainTransactionObserved: %w", err)
	}
	d.metrics.recordEmitted(ctx, ev)
	d.lggr.Debugw("Emitted ChainTransactionObserved", LogKVs(ev)...)
	return nil
}

type emitterMetrics struct {
	emitted metric.Int64Counter
	errors  metric.Int64Counter
}

func newEmitterMetrics() (*emitterMetrics, error) {
	meter := beholder.GetMeter()
	emitted, err := meter.Int64Counter("chain_capability_tx_events_emitted_count",
		metric.WithDescription("Count of ChainTransactionObserved events accepted by the durable emitter"))
	if err != nil {
		return nil, fmt.Errorf("failed to create tx events emitted counter: %w", err)
	}
	errs, err := meter.Int64Counter("chain_capability_tx_events_emit_error_count",
		metric.WithDescription("Count of ChainTransactionObserved events that could not be emitted, by reason"))
	if err != nil {
		return nil, fmt.Errorf("failed to create tx events error counter: %w", err)
	}
	return &emitterMetrics{emitted: emitted, errors: errs}, nil
}

func (m *emitterMetrics) recordEmitted(ctx context.Context, ev *ChainTransactionObserved) {
	m.emitted.Add(ctx, 1, metric.WithAttributes(eventAttrs(ev)...))
}

func (m *emitterMetrics) recordError(ctx context.Context, ev *ChainTransactionObserved, reason string) {
	m.errors.Add(ctx, 1, metric.WithAttributes(append(eventAttrs(ev), attribute.String("reason", reason))...))
}

func eventAttrs(ev *ChainTransactionObserved) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("chain_selector", strconv.FormatUint(ev.GetChainSelector(), 10)),
		attribute.String("tx_status", ev.GetTxStatus().String()),
	}
}
