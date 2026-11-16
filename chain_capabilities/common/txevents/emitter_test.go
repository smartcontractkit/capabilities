package txevents

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/durableemitter"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func testEvent() *ChainTransactionObserved {
	return &ChainTransactionObserved{
		WorkflowExecutionId: "exec-1",
		WorkflowId:          "wf-1",
		ChainSelector:       16015286601757825753,
		TxHash:              "0xabc",
		TxStatus:            TxStatus_TX_STATUS_REVERTED,
		ObservedAtMs:        1,
	}
}

func TestEntityType(t *testing.T) {
	require.Equal(t, "chain_capabilities.txevents.v1.ChainTransactionObserved", EntityType())
}

func TestKey(t *testing.T) {
	ev := testEvent()
	require.Equal(t, "16015286601757825753:0xabc", Key(ev))

	// The key depends only on the transaction, not on who observed it, when, or what it reported.
	other := proto.Clone(ev).(*ChainTransactionObserved)
	other.ObservedAtMs = 42
	other.TxStatus = TxStatus_TX_STATUS_SUCCESS
	other.WorkflowExecutionId = "exec-2"
	require.Equal(t, Key(ev), Key(other))

	other.TxHash = "0xdef"
	require.NotEqual(t, Key(ev), Key(other))
}

func TestGlobalSink(t *testing.T) {
	// The test process never sets up a durable emitter, which is exactly the situation of a node
	// running without CL_CHIP_INGRESS_DURABLE_EMITTER_ENABLED.
	require.Nil(t, durableemitter.GetGlobalEmitter())
	err := GlobalSink().Emit(t.Context(), []byte("body"), "source", DefaultDomain)
	require.ErrorIs(t, err, ErrEmitterUnavailable)
}

func TestDurableEmitter_Emit(t *testing.T) {
	newEmitter := func(t *testing.T, emit SinkFunc) *DurableEmitter {
		t.Helper()
		d, err := NewDurableEmitter(logger.Test(t), "", emit)
		require.NoError(t, err)
		require.Equal(t, DefaultDomain, d.domain)
		return d
	}

	t.Run("requires a sink", func(t *testing.T) {
		_, err := NewDurableEmitter(logger.Test(t), "", nil)
		require.Error(t, err)
	})

	t.Run("passes source, type, keys and the marshalled body", func(t *testing.T) {
		var gotBody []byte
		var gotAttrs []any
		d := newEmitter(t, func(_ context.Context, body []byte, attrKVs ...any) error {
			gotBody, gotAttrs = body, attrKVs
			return nil
		})
		ev := testEvent()
		require.NoError(t, d.Emit(t.Context(), ev))

		var decoded ChainTransactionObserved
		require.NoError(t, proto.Unmarshal(gotBody, &decoded))
		require.True(t, proto.Equal(ev, &decoded))
		require.Equal(t, []any{
			"source", DefaultDomain,
			"type", EntityType(),
			"idempotencykey", Key(ev),
			"subject", Key(ev),
		}, gotAttrs)
	})

	t.Run("bounds the emit context", func(t *testing.T) {
		d := newEmitter(t, func(ctx context.Context, _ []byte, _ ...any) error {
			_, ok := ctx.Deadline()
			require.True(t, ok, "emit context must carry a deadline")
			return nil
		})
		require.NoError(t, d.Emit(t.Context(), testEvent()))
	})

	t.Run("reports ErrEmitterUnavailable when the process has no durable emitter", func(t *testing.T) {
		d, err := NewDurableEmitter(logger.Test(t), "custom-domain", GlobalSink())
		require.NoError(t, err)
		require.Equal(t, "custom-domain", d.domain)
		require.ErrorIs(t, d.Emit(t.Context(), testEvent()), ErrEmitterUnavailable)
		require.ErrorIs(t, d.Emit(t.Context(), testEvent()), ErrEmitterUnavailable)
		require.True(t, d.unavailableLogged.Load())
	})

	t.Run("wraps other emit errors", func(t *testing.T) {
		cause := errors.New("db down")
		d := newEmitter(t, func(context.Context, []byte, ...any) error { return cause })
		err := d.Emit(t.Context(), testEvent())
		require.ErrorIs(t, err, cause)
		require.NotErrorIs(t, err, ErrEmitterUnavailable)
		require.False(t, d.unavailableLogged.Load())
	})

	t.Run("rejects a nil event", func(t *testing.T) {
		d := newEmitter(t, func(context.Context, []byte, ...any) error { return nil })
		require.Error(t, d.Emit(t.Context(), nil))
	})
}

func TestNopEmitter(t *testing.T) {
	require.NoError(t, NopEmitter{}.Emit(t.Context(), testEvent()))
	require.NoError(t, NopEmitter{}.Emit(t.Context(), nil))
}
