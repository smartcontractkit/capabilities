package internal

import (
	"context"
	"testing"
	"time"

	"github.com/smartcontractkit/capabilities/libs/testutils"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/mock/internal/pb"
	"github.com/smartcontractkit/capabilities/mock/utils"
)

type mockCapRegistry struct {
	caps map[string]capabilities.BaseCapability
}

var _ core.CapabilitiesRegistry = (*mockCapRegistry)(nil)

func newMockCapRegistry() *mockCapRegistry {
	return &mockCapRegistry{
		caps: make(map[string]capabilities.BaseCapability),
	}
}

func (m *mockCapRegistry) LocalNode(ctx context.Context) (capabilities.Node, error) {
	return capabilities.Node{}, nil
}

func (m *mockCapRegistry) ConfigForCapability(ctx context.Context, capabilityID string, donID uint32) (capabilities.CapabilityConfiguration, error) {
	return capabilities.CapabilityConfiguration{}, nil
}

func (m *mockCapRegistry) Remove(ctx context.Context, ID string) error {
	return nil
}

func (m *mockCapRegistry) Add(ctx context.Context, capability capabilities.BaseCapability) error {
	info, err := capability.Info(ctx)
	if err != nil {
		return err
	}
	m.caps[info.ID] = capability
	return nil
}

func (m *mockCapRegistry) Get(ctx context.Context, id string) (capabilities.BaseCapability, error) {
	return m.caps[id], nil
}

func (m *mockCapRegistry) List(ctx context.Context) ([]capabilities.BaseCapability, error) {
	var caps []capabilities.BaseCapability
	for _, cap := range m.caps {
		caps = append(caps, cap)
	}
	return caps, nil
}

func (m *mockCapRegistry) GetConsensus(ctx context.Context, id string) (capabilities.ConsensusCapability, error) {
	capability := m.caps[id]
	if cons, ok := capability.(capabilities.ConsensusCapability); ok {
		return cons, nil
	}
	return nil, nil
}

func (m *mockCapRegistry) GetAction(ctx context.Context, id string) (capabilities.ActionCapability, error) {
	capability := m.caps[id]
	if action, ok := capability.(capabilities.ActionCapability); ok {
		return action, nil
	}
	return nil, nil
}

func (m *mockCapRegistry) GetTarget(ctx context.Context, id string) (capabilities.TargetCapability, error) {
	capability := m.caps[id]
	if target, ok := capability.(capabilities.TargetCapability); ok {
		return target, nil
	}
	return nil, nil
}

func (m *mockCapRegistry) GetTrigger(ctx context.Context, id string) (capabilities.TriggerCapability, error) {
	capability := m.caps[id]
	if trigger, ok := capability.(capabilities.TriggerCapability); ok {
		return trigger, nil
	}
	return nil, nil
}

func TestMockRegistry_CreateCapabilities(t *testing.T) {
	lggr := testutils.NewLogger(t)
	capRegistry := newMockCapRegistry()
	registry := NewMockRegistry(lggr, capRegistry)

	ctx := context.Background()

	t.Run("create trigger capability", func(t *testing.T) {
		info := &pb.CapabilityInfo{
			ID:             "test-trigger",
			CapabilityType: pb.CapabilityType_Trigger,
			Description:    "test trigger",
			IsLocal:        true,
		}

		_, err := registry.CreateCapability(ctx, info)
		require.NoError(t, err)
		require.Contains(t, registry.Triggers, "test-trigger")
	})

	t.Run("create target capability", func(t *testing.T) {
		info := &pb.CapabilityInfo{
			ID:             "test-target",
			CapabilityType: pb.CapabilityType_Target,
			Description:    "test target",
			IsLocal:        true,
		}

		_, err := registry.CreateCapability(ctx, info)
		require.NoError(t, err)
		require.Contains(t, registry.Targets, "test-target")
	})

	t.Run("create action capability", func(t *testing.T) {
		info := &pb.CapabilityInfo{
			ID:             "test-action",
			CapabilityType: pb.CapabilityType_Action,
			Description:    "test action",
			IsLocal:        true,
		}

		_, err := registry.CreateCapability(ctx, info)
		require.NoError(t, err)
		require.Contains(t, registry.Action, "test-action")
	})

	t.Run("create consensus capability", func(t *testing.T) {
		info := &pb.CapabilityInfo{
			ID:             "test-consensus",
			CapabilityType: pb.CapabilityType_Consensus,
			Description:    "test consensus",
			IsLocal:        true,
		}

		_, err := registry.CreateCapability(ctx, info)
		require.NoError(t, err)
		require.Contains(t, registry.Consensus, "test-consensus")
	})
}

func TestMockRegistry_SendTriggerEvent(t *testing.T) {
	lggr := testutils.NewLogger(t)
	capRegistry := newMockCapRegistry()
	registry := NewMockRegistry(lggr, capRegistry)

	ctx := context.Background()

	info := &pb.CapabilityInfo{
		ID:             "test-trigger",
		CapabilityType: pb.CapabilityType_Trigger,
	}

	_, err := registry.CreateCapability(ctx, info)
	require.NoError(t, err)

	subscriber := registry.Triggers["test-trigger"]
	ch, err := subscriber.RegisterTrigger(context.Background(), capabilities.TriggerRegistrationRequest{
		TriggerID: "test-trigger",
		Metadata:  capabilities.RequestMetadata{},
		Config:    nil,
	})

	require.NoError(t, err)

	payload := &values.Map{
		Underlying: map[string]values.Value{
			"test": values.NewString("value"),
		},
	}
	payloadBytes, err := utils.MapToBytes(payload)
	require.NoError(t, err)

	go func() {
		_, err = registry.SendTriggerEvent(ctx, &pb.SendTriggerEventRequest{
			ID:      "test-trigger",
			EventID: "event1",
			Payload: payloadBytes,
		})
		require.NoError(t, err)
	}()

	select {
	case resp := <-ch:
		require.Equal(t, "test-trigger", resp.Event.TriggerType)
		require.Equal(t, "event1", resp.Event.ID)
		require.Equal(t, payload, resp.Event.Outputs)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for trigger event")
	}
}

func TestMockRegistry_Execute(t *testing.T) {
	lggr := testutils.NewLogger(t)
	capRegistry := newMockCapRegistry()
	registry := NewMockRegistry(lggr, capRegistry)

	ctx := context.Background()

	info := &pb.CapabilityInfo{
		ID:             "test-target",
		CapabilityType: pb.CapabilityType_Target,
	}

	_, err := registry.CreateCapability(ctx, info)
	require.NoError(t, err)

	inputs := &values.Map{
		Underlying: map[string]values.Value{
			"input": values.NewString("test"),
		},
	}
	inputBytes, err := utils.MapToBytes(inputs)
	require.NoError(t, err)

	config := &values.Map{
		Underlying: map[string]values.Value{
			"config": values.NewString("test"),
		},
	}
	configBytes, err := utils.MapToBytes(config)
	require.NoError(t, err)

	executable := registry.Targets["test-target"]
	executable.ExecuteTimeout = time.Millisecond * 50000
	go func() {
		<-executable.requestChan
		executable.ResponseChan <- capabilities.CapabilityResponse{
			Value: inputs,
		}
	}()

	resp, err := registry.Execute(ctx, &pb.ExecutableRequest{
		ID:             "test-target",
		CapabilityType: pb.CapabilityType_Target,
		RequestMetadata: &pb.Metadata{
			WorkflowID: "test-workflow",
		},
		Inputs: inputBytes,
		Config: configBytes,
	})

	require.NoError(t, err)
	respMap, err := utils.BytesToMap(resp.Value)
	require.NoError(t, err)
	require.Equal(t, inputs, respMap)
}

func TestMockRegistry_List(t *testing.T) {
	lggr := testutils.NewLogger(t)
	capRegistry := newMockCapRegistry()
	registry := NewMockRegistry(lggr, capRegistry)

	ctx := context.Background()

	// Create capabilities of different types
	caps := []*pb.CapabilityInfo{
		{
			ID:             "test-trigger",
			CapabilityType: pb.CapabilityType_Trigger,
			Description:    "test trigger",
		},
		{
			ID:             "test-target",
			CapabilityType: pb.CapabilityType_Target,
			Description:    "test target",
		},
	}

	for _, cap := range caps {
		_, err := registry.CreateCapability(ctx, cap)
		require.NoError(t, err)
	}

	resp, err := registry.List(ctx, &pb.ListRequest{})
	require.NoError(t, err)
	require.Len(t, resp.CapInfos, 2)

	// Verify the capabilities are listed correctly
	for _, info := range resp.CapInfos {
		found := false
		for _, expected := range caps {
			if info.ID == expected.ID {
				require.Equal(t, expected.CapabilityType, info.CapabilityType)
				require.Equal(t, expected.Description, info.Description)
				found = true
				break
			}
		}
		require.True(t, found, "Listed capability not found in original caps")
	}
}
