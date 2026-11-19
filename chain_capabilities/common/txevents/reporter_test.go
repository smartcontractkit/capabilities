package txevents

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	eventspb "github.com/smartcontractkit/chainlink-protos/workflows/go/v2"
)

type recordingEmitter struct {
	events []*eventspb.ChainTransactionObserved
	err    error
}

func (r *recordingEmitter) Emit(_ context.Context, ev *eventspb.ChainTransactionObserved) error {
	r.events = append(r.events, ev)
	return r.err
}

func testMetadata() capabilities.RequestMetadata {
	return capabilities.RequestMetadata{WorkflowID: "wf-id", WorkflowExecutionID: "exec-1"}
}

func TestReporter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("disabled emission yields a nil reporter", func(t *testing.T) {
		lggr := logger.Test(t)
		require.Nil(t, NewReporter(nil, 1, testMetadata(), lggr))
		require.Nil(t, NewReporter(NopEmitter{}, 1, testMetadata(), lggr))
		var r *Reporter
		r.Charged(ctx, "0xabc", eventspb.TxStatus_TX_STATUS_SUCCESS)
	})

	t.Run("emits the charged transaction once", func(t *testing.T) {
		rec := &recordingEmitter{}
		r := NewReporter(rec, 1, testMetadata(), logger.Test(t))
		r.now = func() time.Time { return time.UnixMilli(1_700_000_000_000) }

		r.Charged(ctx, "0xabc", eventspb.TxStatus_TX_STATUS_SUCCESS)
		r.Charged(ctx, "0xdef", eventspb.TxStatus_TX_STATUS_REVERTED)
		r.Charged(ctx, "", eventspb.TxStatus_TX_STATUS_SUCCESS)

		require.Len(t, rec.events, 1)
		want := &eventspb.ChainTransactionObserved{
			WorkflowExecutionId: "exec-1",
			WorkflowId:          "wf-id",
			ChainSelector:       1,
			TxHash:              "0xabc",
			TxStatus:            eventspb.TxStatus_TX_STATUS_SUCCESS,
			ObservedAtMs:        1_700_000_000_000,
		}
		require.True(t, proto.Equal(want, rec.events[0]), "got %v", rec.events[0])
	})

	t.Run("survives emitter errors", func(t *testing.T) {
		rec := &recordingEmitter{err: errors.New("chip down")}
		r := NewReporter(rec, 1, testMetadata(), logger.Test(t))
		r.Charged(ctx, "0xabc", eventspb.TxStatus_TX_STATUS_SUCCESS)
		require.Len(t, rec.events, 1)

		unavailable := NewReporter(EmitterFunc(func(context.Context, *eventspb.ChainTransactionObserved) error {
			return ErrEmitterUnavailable
		}), 1, testMetadata(), logger.Test(t))
		unavailable.Charged(ctx, "0xabc", eventspb.TxStatus_TX_STATUS_SUCCESS)
	})

	t.Run("emits even after the request context is done", func(t *testing.T) {
		var gotCtx context.Context
		r := NewReporter(EmitterFunc(func(ctx context.Context, _ *eventspb.ChainTransactionObserved) error {
			gotCtx = ctx
			return nil
		}), 1, testMetadata(), logger.Test(t))
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		r.Charged(cancelled, "0xabc", eventspb.TxStatus_TX_STATUS_SUCCESS)
		require.NotNil(t, gotCtx)
		require.NoError(t, gotCtx.Err())
	})
}

// EmitterFunc adapts a function to Emitter.
type EmitterFunc func(ctx context.Context, event *eventspb.ChainTransactionObserved) error

// Emit implements Emitter.
func (f EmitterFunc) Emit(ctx context.Context, event *eventspb.ChainTransactionObserved) error {
	return f(ctx, event)
}

func TestNewEmitter(t *testing.T) {
	t.Parallel()

	t.Run("disabled", func(t *testing.T) {
		emitter, err := NewEmitter(logger.Test(t), Config{})
		require.NoError(t, err)
		require.IsType(t, NopEmitter{}, emitter)
	})

	t.Run("enabled without a durable emitter still constructs", func(t *testing.T) {
		emitter, err := NewEmitter(logger.Test(t), Config{Enabled: true})
		require.NoError(t, err)
		require.ErrorIs(t, emitter.Emit(t.Context(), testEvent()), ErrEmitterUnavailable)
	})
}
