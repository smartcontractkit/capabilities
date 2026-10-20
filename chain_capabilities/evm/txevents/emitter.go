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

// ErrEmitterUnavailable is returned when the LOOP has no durable emitter, which is the case when
// the node runs without CL_CHIP_INGRESS_DURABLE_EMITTER_ENABLED. Events are dropped and counted.
var ErrEmitterUnavailable = errors.New("durable emitter not available in this process")

// Sink delivers an encoded event with its routing attributes. chainlink-common's
// durableemitter.DurableEmitter satisfies it; the LOOP server injects that instance through
// resourcemanager.Config (loop.Server.MeteringConfig) so capabilities never reach for the
// process-global emitter.
type Sink interface {
	Emit(ctx context.Context, body []byte, attrKVs ...any) error
}

// Emitter publishes ChainTransactionObserved events.
type Emitter interface {
	Emit(ctx context.Context, event *ChainTransactionObserved) error
}

// NopEmitter discards events. Used when transaction events are disabled.
type NopEmitter struct{}

// Emit implements Emitter.
func (NopEmitter) Emit(context.Context, *ChainTransactionObserved) error { return nil }

// IdempotencyKey identifies a transaction at a detail level independently of the node that
// observed it, so that every node that learnt the same about a transaction produces the same key
// and ChIP or the Blockchain Finality Service can dedup on it. A node whose receipt lookup failed
// produces a different key than its peers, so its basic event does not shadow their receipt
// detail; the finality service dedups by (chain selector, hash) regardless.
func IdempotencyKey(ev *ChainTransactionObserved) string {
	return fmt.Sprintf("%d:%s:%s", ev.GetChainSelector(), ev.GetTxHash(), ev.GetDetail().String())
}

// Subject identifies the transaction itself and is set as the CloudEvent subject.
func Subject(ev *ChainTransactionObserved) string {
	return fmt.Sprintf("%d:%s", ev.GetChainSelector(), ev.GetTxHash())
}

// LogKVs returns the event's identifying fields as logger key/values.
func LogKVs(ev *ChainTransactionObserved) []any {
	return []any{
		"txHash", ev.GetTxHash(),
		"chainSelector", ev.GetChainSelector(),
		"detail", ev.GetDetail().String(),
		"txStatus", ev.GetTxStatus().String(),
		"receiverStatus", ev.GetReceiverStatus().String(),
		"transmitter", ev.GetTransmitter(),
		"duplicate", ev.GetDuplicate(),
		"workflowExecutionID", ev.GetWorkflowExecutionId(),
		"referenceID", ev.GetReferenceId(),
	}
}

// DurableEmitter publishes events through the LOOP's durable emitter, which persists them in the
// node database and delivers them to ChIP Ingress at least once.
type DurableEmitter struct {
	lggr    logger.Logger
	domain  string
	sink    Sink
	metrics *emitterMetrics

	unavailableLogged atomic.Bool
}

// NewDurableEmitter returns an emitter publishing to the given ChIP source domain through sink.
// A nil sink means the LOOP has no durable emitter: events are dropped, counted and the
// condition is logged once.
func NewDurableEmitter(lggr logger.Logger, domain string, sink Sink) (*DurableEmitter, error) {
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

// Emit implements Emitter.
func (d *DurableEmitter) Emit(ctx context.Context, ev *ChainTransactionObserved) error {
	if ev == nil {
		return errors.New("nil ChainTransactionObserved")
	}
	if d.sink == nil {
		d.metrics.recordError(ctx, ev, "unavailable")
		if d.unavailableLogged.CompareAndSwap(false, true) {
			d.lggr.Warnw("Transaction events are enabled but this LOOP has no durable emitter; events are dropped", "domain", d.domain)
		}
		return ErrEmitterUnavailable
	}
	body, err := proto.Marshal(ev)
	if err != nil {
		d.metrics.recordError(ctx, ev, "marshal")
		return fmt.Errorf("marshal ChainTransactionObserved: %w", err)
	}

	emitCtx, cancel := context.WithTimeout(ctx, emitTimeout)
	defer cancel()
	err = d.sink.Emit(emitCtx, body,
		attrSource, d.domain,
		attrType, EntityType(),
		attrIdempotencyKey, IdempotencyKey(ev),
		attrSubject, Subject(ev),
	)
	if err != nil {
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
		attribute.String("detail", ev.GetDetail().String()),
		attribute.String("tx_status", ev.GetTxStatus().String()),
		attribute.String("receiver_status", ev.GetReceiverStatus().String()),
		attribute.Bool("transmitter", ev.GetTransmitter()),
		attribute.Bool("duplicate", ev.GetDuplicate()),
	}
}
