package capcommon

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

type testConsensusHandler struct {
	handle func(context.Context, ctypes.Request) (<-chan ctypes.Reply, error)
}

func (h testConsensusHandler) Handle(ctx context.Context, request ctypes.Request) (<-chan ctypes.Reply, error) {
	return h.handle(ctx, request)
}

func TestReadType(t *testing.T) {
	t.Run("handler returns error", func(t *testing.T) {
		expected := errors.New("boom")
		_, err := ReadType[int](t.Context(), testConsensusHandler{
			handle: func(context.Context, ctypes.Request) (<-chan ctypes.Reply, error) {
				return make(chan ctypes.Reply), expected
			},
		}, ctypes.NewAggregatableRequest("id", nil))
		require.ErrorIs(t, err, expected)
	})

	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := ReadType[int](ctx, testConsensusHandler{
			handle: func(context.Context, ctypes.Request) (<-chan ctypes.Reply, error) {
				return make(chan ctypes.Reply), nil
			},
		}, ctypes.NewAggregatableRequest("id", nil))
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("reply returns error", func(t *testing.T) {
		expected := errors.New("reply failed")
		ch := make(chan ctypes.Reply, 1)
		ch <- ctypes.Reply{Err: expected}

		_, err := ReadType[int](t.Context(), testConsensusHandler{
			handle: func(context.Context, ctypes.Request) (<-chan ctypes.Reply, error) {
				return ch, nil
			},
		}, ctypes.NewAggregatableRequest("id", nil))
		require.ErrorIs(t, err, expected)
	})

	t.Run("type mismatch", func(t *testing.T) {
		ch := make(chan ctypes.Reply, 1)
		ch <- ctypes.Reply{Value: "not-an-int"}

		_, err := ReadType[int](t.Context(), testConsensusHandler{
			handle: func(context.Context, ctypes.Request) (<-chan ctypes.Reply, error) {
				return ch, nil
			},
		}, ctypes.NewAggregatableRequest("id", nil))
		require.ErrorContains(t, err, "unexpected result type")
	})

	t.Run("happy path", func(t *testing.T) {
		ch := make(chan ctypes.Reply, 1)
		ch <- ctypes.Reply{Value: 16}

		result, err := ReadType[int](t.Context(), testConsensusHandler{
			handle: func(context.Context, ctypes.Request) (<-chan ctypes.Reply, error) {
				return ch, nil
			},
		}, ctypes.NewAggregatableRequest("id", nil))
		require.NoError(t, err)
		require.Equal(t, 16, result)
	})
}
