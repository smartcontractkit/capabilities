package internal

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/testutils"

	"github.com/smartcontractkit/capabilities/mock/internal/pb"
)

func TestTrigger_RegisterAndUnregister(t *testing.T) {
	lggr := testutils.NewLogger(t)
	info := &pb.CapabilityInfo{
		ID:             "test-trigger",
		CapabilityType: pb.CapabilityType_Trigger,
		Description:    "test trigger",
		IsLocal:        true,
	}
	trigger := NewTrigger(info, lggr)

	t.Run("register new trigger", func(t *testing.T) {
		req := capabilities.TriggerRegistrationRequest{
			TriggerID: "trigger1",
		}
		ch, err := trigger.RegisterTrigger(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, ch)
		assert.Len(t, trigger.Subscribers, 1)
	})

	t.Run("register duplicate trigger", func(t *testing.T) {
		req := capabilities.TriggerRegistrationRequest{
			TriggerID: "trigger1",
		}
		ch, err := trigger.RegisterTrigger(context.Background(), req)
		require.Error(t, err)
		require.Nil(t, ch)
		assert.Contains(t, err.Error(), "already registered")
	})

	t.Run("unregister existing trigger", func(t *testing.T) {
		req := capabilities.TriggerRegistrationRequest{
			TriggerID: "trigger1",
		}
		err := trigger.UnregisterTrigger(context.Background(), req)
		require.NoError(t, err)
		assert.Len(t, trigger.Subscribers, 0)
	})

	t.Run("unregister non-existing trigger", func(t *testing.T) {
		req := capabilities.TriggerRegistrationRequest{
			TriggerID: "nonexistent",
		}
		err := trigger.UnregisterTrigger(context.Background(), req)
		require.NoError(t, err)
	})
}

func TestTrigger_ConcurrentAccess(t *testing.T) {
	lggr := testutils.NewLogger(t)
	info := &pb.CapabilityInfo{
		ID:             "test-trigger",
		CapabilityType: pb.CapabilityType_Trigger,
		Description:    "test concurrent",
		IsLocal:        true,
	}
	trigger := NewTrigger(info, lggr)

	var wg sync.WaitGroup
	concurrent := 10

	// Test concurrent registrations
	for i := range concurrent {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := capabilities.TriggerRegistrationRequest{
				TriggerID: fmt.Sprintf("trigger-%d", id),
			}
			ch, err := trigger.RegisterTrigger(context.Background(), req)
			if err == nil {
				require.NotNil(t, ch)
			}
		}(i)
	}
	wg.Wait()

	// Verify registrations
	trigger.mu.RLock()
	assert.LessOrEqual(t, len(trigger.Subscribers), concurrent)
	trigger.mu.RUnlock()

	// Test concurrent unregistrations
	for i := range concurrent {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := capabilities.TriggerRegistrationRequest{
				TriggerID: fmt.Sprintf("trigger-%d", id),
			}
			_ = trigger.UnregisterTrigger(context.Background(), req)
		}(i)
	}
	wg.Wait()

	trigger.mu.RLock()
	assert.Empty(t, trigger.Subscribers)
	trigger.mu.RUnlock()
}

func TestTrigger_ChannelBuffering(t *testing.T) {
	lggr := testutils.NewLogger(t)
	info := &pb.CapabilityInfo{
		ID:             "test-trigger",
		CapabilityType: pb.CapabilityType_Trigger,
		Description:    "test buffering",
		IsLocal:        true,
	}
	trigger := NewTrigger(info, lggr)

	req := capabilities.TriggerRegistrationRequest{
		TriggerID: "trigger1",
	}
	ch, err := trigger.RegisterTrigger(context.Background(), req)
	require.NoError(t, err)

	// Verify channel buffer size
	trigger.mu.RLock()
	sub := trigger.Subscribers["trigger1"]
	trigger.mu.RUnlock()

	// Test that the channel is buffered to 1000
	for i := range 1000 {
		sub.Ch <- capabilities.TriggerResponse{
			Event: capabilities.TriggerEvent{
				ID: fmt.Sprintf("event-%d", i),
			},
		}
	}

	// Verify we can receive all messages
	for range 1000 {
		select {
		case resp := <-ch:
			assert.Contains(t, resp.Event.ID, "event-")
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for buffered message")
		}
	}
}
