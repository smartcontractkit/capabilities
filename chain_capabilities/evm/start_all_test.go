package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func TestStartAll(t *testing.T) {
	var events []string
	svc := func(name string, startErr error) serviceManager {
		return serviceManager{
			name:  name,
			start: func(context.Context) error { events = append(events, "start "+name); return startErr },
			close: func() error { events = append(events, "close "+name); return errors.New("close errors are logged, not returned") },
		}
	}

	t.Run("closes already-started services in reverse order on failure", func(t *testing.T) {
		events = nil
		startErr := errors.New("failed to list pending events: context deadline exceeded")

		err := startAll(t.Context(), logger.Test(t), []serviceManager{svc("a", nil), svc("b", nil), svc("trigger", startErr), svc("never", nil)})

		require.ErrorIs(t, err, startErr)
		require.Equal(t, []string{"start a", "start b", "start trigger", "close b", "close a"}, events)
	})

	t.Run("closes nothing on success", func(t *testing.T) {
		events = nil

		require.NoError(t, startAll(t.Context(), logger.Test(t), []serviceManager{svc("a", nil), svc("b", nil)}))
		require.Equal(t, []string{"start a", "start b"}, events)
	})
}
