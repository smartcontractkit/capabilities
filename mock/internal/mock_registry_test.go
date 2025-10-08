package internal

import (
	"context"
	"testing"
	"time"

	"github.com/smartcontractkit/libocr/ragep2p/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/capabilities/libs/testutils"

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

func (m *mockCapRegistry) Get(_ context.Context, id string) (capabilities.BaseCapability, error) {
	return m.caps[id], nil
}

func (m *mockCapRegistry) List(_ context.Context) ([]capabilities.BaseCapability, error) {
	var caps []capabilities.BaseCapability
	for _, cap := range m.caps {
		caps = append(caps, cap)
	}
	return caps, nil
}

func (m *mockCapRegistry) GetTrigger(_ context.Context, id string) (capabilities.TriggerCapability, error) {
	capability := m.caps[id]
	if trigger, ok := capability.(capabilities.TriggerCapability); ok {
		return trigger, nil
	}
	return nil, nil
}

func (m *mockCapRegistry) NodeByPeerID(_ context.Context, peerID types.PeerID) (capabilities.Node, error) {
	return capabilities.Node{}, nil
}

func (m *mockCapRegistry) GetExecutable(_ context.Context, id string) (capabilities.ExecutableCapability, error) {
	capability := m.caps[id]
	if target, ok := capability.(capabilities.ExecutableCapability); ok {
		return target, nil
	}
	return nil, nil
}

func (m *mockCapRegistry) DONsForCapability(_ context.Context, capabilityID string) ([]capabilities.DONWithNodes, error) {
	return []capabilities.DONWithNodes{}, nil
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
		require.Contains(t, registry.Executables, "test-target")
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
		require.Contains(t, registry.Executables, "test-action")
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
		require.Contains(t, registry.Executables, "test-consensus")
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

	outputs := &values.Map{
		Underlying: map[string]values.Value{
			"test": values.NewString("value"),
		},
	}
	outputsBytes, err := utils.MapToBytes(outputs)
	require.NoError(t, err)
	payload := &anypb.Any{
		TypeUrl: "some-type",
		Value:   []byte("some-payload"),
	}
	ocrTriggerEvent := capabilities.OCRTriggerEvent{
		ConfigDigest: []byte("ocr-config-digest"),
		SeqNr:        32156,
		Report:       []byte("ocr-report"),
		Sigs: []capabilities.OCRAttributedOnchainSignature{
			{
				Signature: []byte("ocr-signature-1"),
				Signer:    0,
			},
			{
				Signature: []byte("ocr-signature-2"),
				Signer:    1,
			},
		},
	}

	go func() {
		_, err = registry.SendTriggerEvent(ctx, &pb.SendTriggerEventRequest{
			TriggerID:   "test-trigger",
			TriggerType: "type-trigger",
			ID:          "event1",
			Outputs:     outputsBytes,
			Payload:     payload,
			OCREvent: &pb.OCRTriggerEvent{
				ConfigDigest: ocrTriggerEvent.ConfigDigest,
				SeqNr:        ocrTriggerEvent.SeqNr,
				Report:       ocrTriggerEvent.Report,
				Sigs: []*pb.OCRAttributedOnchainSignature{
					{Signature: ocrTriggerEvent.Sigs[0].Signature,
						Signer: ocrTriggerEvent.Sigs[0].Signer,
					},
					{
						Signature: ocrTriggerEvent.Sigs[1].Signature,
						Signer:    ocrTriggerEvent.Sigs[1].Signer,
					},
				},
			},
		})
		require.NoError(t, err)
	}()

	select {
	case resp := <-ch:
		require.Equal(t, "type-trigger", resp.Event.TriggerType)
		require.Equal(t, "event1", resp.Event.ID)
		require.Equal(t, outputs, resp.Event.Outputs)
		require.Equal(t, payload, resp.Event.Payload)
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

	executable := registry.Executables["test-target"]
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

	for _, c := range caps {
		_, err := registry.CreateCapability(ctx, c)
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

func TestMockRegistry_GetTriggerSubscribers(t *testing.T) {
	lggr := testutils.NewLogger(t)
	capRegistry := newMockCapRegistry()
	registry := NewMockRegistry(lggr, capRegistry)

	ctx := context.Background()

	// Create a trigger capability
	info := &pb.CapabilityInfo{
		ID:             "test-trigger",
		CapabilityType: pb.CapabilityType_Trigger,
	}

	_, err := registry.CreateCapability(ctx, info)
	require.NoError(t, err)

	// Register two subscribers with different workflow IDs
	trigger := registry.Triggers["test-trigger"]

	// First subscriber
	_, err = trigger.RegisterTrigger(ctx, capabilities.TriggerRegistrationRequest{
		TriggerID: "workflow-1",
	})
	require.NoError(t, err)

	// Second subscriber
	_, err = trigger.RegisterTrigger(ctx, capabilities.TriggerRegistrationRequest{
		TriggerID: "workflow-2",
	})
	require.NoError(t, err)

	// Test case 1: Getting subscribers for a trigger with subscribers
	t.Run("existing trigger with subscribers", func(t *testing.T) {
		resp, err := registry.GetTriggerSubscribers(ctx, &pb.GetTriggerSubscribersRequest{
			ID: "test-trigger",
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.WorkflowIDs, 2)
		require.Contains(t, resp.WorkflowIDs, "workflow-1")
		require.Contains(t, resp.WorkflowIDs, "workflow-2")
	})

	// Test case 2: Getting subscribers for a non-existent trigger
	t.Run("non-existent trigger", func(t *testing.T) {
		resp, err := registry.GetTriggerSubscribers(ctx, &pb.GetTriggerSubscribersRequest{
			ID: "non-existent-trigger",
		})

		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "not found")
	})

	// Test case 3: Getting subscribers for a trigger with no subscribers
	t.Run("trigger with no subscribers", func(t *testing.T) {
		emptyInfo := &pb.CapabilityInfo{
			ID:             "empty-trigger",
			CapabilityType: pb.CapabilityType_Trigger,
		}

		_, err := registry.CreateCapability(ctx, emptyInfo)
		require.NoError(t, err)

		resp, err := registry.GetTriggerSubscribers(ctx, &pb.GetTriggerSubscribersRequest{
			ID: "empty-trigger",
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Empty(t, resp.WorkflowIDs)
	})
}

func TestMockRegistry_RemoveCapability(t *testing.T) {
	t.Parallel()
	lggr := testutils.NewLogger(t)
	capRegistry := newMockCapRegistry()
	registry := NewMockRegistry(lggr, capRegistry)

	ctx := context.Background()

	// Create capabilities of different types
	triggerInfo := &pb.CapabilityInfo{
		ID:             "test-trigger",
		CapabilityType: pb.CapabilityType_Trigger,
		Description:    "test trigger",
		IsLocal:        true,
	}

	targetInfo := &pb.CapabilityInfo{
		ID:             "test-target",
		CapabilityType: pb.CapabilityType_Target,
		Description:    "test target",
		IsLocal:        true,
	}

	actionInfo := &pb.CapabilityInfo{
		ID:             "test-action",
		CapabilityType: pb.CapabilityType_Action,
		Description:    "test action",
		IsLocal:        true,
	}

	// Create the capabilities
	_, err := registry.CreateCapability(ctx, triggerInfo)
	require.NoError(t, err)
	require.Contains(t, registry.Triggers, "test-trigger")

	_, err = registry.CreateCapability(ctx, targetInfo)
	require.NoError(t, err)
	require.Contains(t, registry.Executables, "test-target")

	_, err = registry.CreateCapability(ctx, actionInfo)
	require.NoError(t, err)
	require.Contains(t, registry.Executables, "test-action")

	// Test removing trigger capability
	removeReq := &pb.RemoveCapabilityRequest{
		ID: "test-trigger",
	}

	_, err = registry.RemoveCapability(ctx, removeReq)
	require.NoError(t, err)
	require.NotContains(t, registry.Triggers, "test-trigger")

	// Test removing target capability
	removeReq = &pb.RemoveCapabilityRequest{
		ID: "test-target",
	}

	_, err = registry.RemoveCapability(ctx, removeReq)
	require.NoError(t, err)
	require.NotContains(t, registry.Executables, "test-target")

	// Test removing action capability
	removeReq = &pb.RemoveCapabilityRequest{
		ID: "test-action",
	}

	_, err = registry.RemoveCapability(ctx, removeReq)
	require.NoError(t, err)
	require.NotContains(t, registry.Executables, "test-action")

	// Test removing non-existent capability
	removeReq = &pb.RemoveCapabilityRequest{
		ID: "non-existent-id",
	}

	_, err = registry.RemoveCapability(ctx, removeReq)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestMockRegistry_SendTriggerEvent_IncompleteData(t *testing.T) {
	t.Parallel()
	lggr := testutils.NewLogger(t)
	capRegistry := newMockCapRegistry()
	registry := NewMockRegistry(lggr, capRegistry)

	ctx := context.Background()

	// Create a trigger capability
	info := &pb.CapabilityInfo{
		ID:             "test-trigger",
		CapabilityType: pb.CapabilityType_Trigger,
	}

	_, err := registry.CreateCapability(ctx, info)
	require.NoError(t, err)

	subscriber := registry.Triggers["test-trigger"]

	// Test case 1: Missing outputs
	t.Run("missing outputs", func(t *testing.T) {
		ch, err := subscriber.RegisterTrigger(context.Background(), capabilities.TriggerRegistrationRequest{
			TriggerID: "test-trigger-no-outputs",
			Metadata:  capabilities.RequestMetadata{},
		})
		require.NoError(t, err)

		// Wait a moment to ensure registration is complete
		time.Sleep(10 * time.Millisecond)

		// Send event in the same goroutine to avoid race
		_, err = registry.SendTriggerEvent(ctx, &pb.SendTriggerEventRequest{
			TriggerID:   "test-trigger",
			TriggerType: "type-trigger",
			ID:          "event-no-outputs",
			// Outputs field intentionally omitted
		})
		require.NoError(t, err)

		select {
		case resp := <-ch:
			require.Equal(t, "type-trigger", resp.Event.TriggerType)
			require.Equal(t, "event-no-outputs", resp.Event.ID)
			require.Equal(t, values.EmptyMap(), resp.Event.Outputs) // Should be empty
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for trigger event")
		}
	})

	// Test case 2: Missing payload
	t.Run("missing payload", func(t *testing.T) {
		ch, err := subscriber.RegisterTrigger(context.Background(), capabilities.TriggerRegistrationRequest{
			TriggerID: "test-trigger-no-payload",
			Metadata:  capabilities.RequestMetadata{},
		})
		require.NoError(t, err)

		// Wait a moment to ensure registration is complete
		time.Sleep(10 * time.Millisecond)

		outputs := &values.Map{
			Underlying: map[string]values.Value{
				"test": values.NewString("value"),
			},
		}
		outputsBytes, err := utils.MapToBytes(outputs)
		require.NoError(t, err)

		// Send event in the same goroutine to avoid race
		_, err = registry.SendTriggerEvent(ctx, &pb.SendTriggerEventRequest{
			TriggerID:   "test-trigger",
			TriggerType: "type-trigger",
			ID:          "event-no-payload",
			Outputs:     outputsBytes,
			// Payload field intentionally omitted
		})
		require.NoError(t, err)

		select {
		case resp := <-ch:
			require.Equal(t, "type-trigger", resp.Event.TriggerType)
			require.Equal(t, "event-no-payload", resp.Event.ID)
			require.Equal(t, outputs, resp.Event.Outputs)
			require.Nil(t, resp.Event.Payload) // Should be nil
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for trigger event")
		}
	})

	// Test case 3: Missing OCR event
	t.Run("missing OCR event", func(t *testing.T) {
		ch, err := subscriber.RegisterTrigger(context.Background(), capabilities.TriggerRegistrationRequest{
			TriggerID: "test-trigger-no-ocr",
			Metadata:  capabilities.RequestMetadata{},
		})
		require.NoError(t, err)

		// Wait a moment to ensure registration is complete
		time.Sleep(10 * time.Millisecond)

		outputs := &values.Map{
			Underlying: map[string]values.Value{
				"test": values.NewString("value"),
			},
		}
		outputsBytes, err := utils.MapToBytes(outputs)
		require.NoError(t, err)

		payload := &anypb.Any{
			TypeUrl: "some-type",
			Value:   []byte("some-payload"),
		}

		// Send event in the same goroutine to avoid race
		_, err = registry.SendTriggerEvent(ctx, &pb.SendTriggerEventRequest{
			TriggerID:   "test-trigger",
			TriggerType: "type-trigger",
			ID:          "event-no-ocr",
			Outputs:     outputsBytes,
			Payload:     payload,
			// OCREvent field intentionally omitted
		})
		require.NoError(t, err)

		select {
		case resp := <-ch:
			require.Equal(t, "type-trigger", resp.Event.TriggerType)
			require.Equal(t, "event-no-ocr", resp.Event.ID)
			require.Equal(t, outputs, resp.Event.Outputs)
			require.Equal(t, payload, resp.Event.Payload)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for trigger event")
		}
	})
}
