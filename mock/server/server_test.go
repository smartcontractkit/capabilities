package server

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/freeport"

	"github.com/smartcontractkit/capabilities/libs/testutils"
	"github.com/smartcontractkit/capabilities/mock/internal/pb"
	"github.com/smartcontractkit/capabilities/mock/utils"
)

func Test_ServerTrigger(t *testing.T) {
	t.Parallel()
	port := freeport.GetOne(t)
	logger := testutils.NewLogger(t)
	capabilitiesRegistry := testutils.NewCapabilitiesRegistry(t)
	capabilitiesServer := New(logger)
	require.NotNil(t, capabilitiesServer)
	// Timeout is important to avoid hanging tests
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	servicetest.RunHealthy(t, capabilitiesServer)

	require.Equal(t, capabilitiesServer.Name(), "MockServer", "server name should be MockServer")

	require.NoError(t, capabilitiesServer.Initialise(ctx, core.StandardCapabilitiesDependencies{
		Config: fmt.Sprintf(`
port=%d
[[DefaultMocks]]
id="some-trigger@1.0.0"
description="bogus trigger description"
type="trigger"
`, port),
		CapabilityRegistry: capabilitiesRegistry,
	}))

	// Create trigger
	_, err := capabilitiesServer.MockRegistry.CreateCapability(ctx, &pb.CapabilityInfo{
		ID:             "some-other-trigger@1.0.0",
		CapabilityType: 1,
		Description:    "bogus other trigger description",
		DON:            nil,
		IsLocal:        true,
	})
	require.NoError(t, err)

	capabilitiesInfos, err := capabilitiesServer.Infos(ctx)
	require.NoError(t, err)
	require.Len(t, capabilitiesInfos, 2)

	require.ElementsMatch(t, capabilitiesInfos, []capabilities.CapabilityInfo{
		{
			ID:             "some-trigger@1.0.0",
			CapabilityType: "trigger",
			Description:    "bogus trigger description",
			DON:            nil,
			IsLocal:        true,
		},
		{
			ID:             "some-other-trigger@1.0.0",
			CapabilityType: "trigger",
			Description:    "bogus other trigger description",
			DON:            nil,
			IsLocal:        true,
		},
	})

	require.NoError(t, capabilitiesRegistry.Contains([]string{"some-trigger@1.0.0"}))
	require.NoError(t, capabilitiesRegistry.Contains([]string{"some-other-trigger@1.0.0"}))

	// Register to trigger
	r1, err := capabilitiesRegistry.GetTrigger(ctx, "some-trigger@1.0.0")
	require.NoError(t, err)
	r1Chan, err := r1.RegisterTrigger(ctx, capabilities.TriggerRegistrationRequest{
		TriggerID: "some-trigger-id",
		Metadata:  capabilities.RequestMetadata{},
		Config:    nil,
	})
	require.NoError(t, err)

	e, err := values.NewMap(map[string]int{"some-key": 4231})
	require.NoError(t, err)
	outputs, err := utils.MapToBytes(e)
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

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		for {
			m := <-r1Chan
			require.Equal(t, m.Event.Outputs, e)
			return
		}
	}()

	// Connect to grpc server
	client, err := connectWithRetry(port, 5)
	require.NoError(t, err)

	_, err = client.SendTriggerEvent(ctx, &pb.SendTriggerEventRequest{
		TriggerID: "some-trigger@1.0.0",
		ID:        "eventID",
		Outputs:   outputs,
		Payload:   payload,
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

	wg.Wait()
}

func Test_ServerExecutable(t *testing.T) {
	t.Parallel()
	port := freeport.GetOne(t)
	logger := testutils.NewLogger(t)
	capabilitiesRegistry := testutils.NewCapabilitiesRegistry(t)
	capabilitiesServer := &MockServer{Lggr: logger}
	require.NotNil(t, capabilitiesServer)

	// Timeout is important to avoid hanging tests
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	servicetest.RunHealthy(t, capabilitiesServer)

	require.NoError(t, capabilitiesServer.Initialise(ctx, core.StandardCapabilitiesDependencies{
		Config: fmt.Sprintf(`
port=%d
[[DefaultMocks]]
id="some-target@1.0.0"
description="bogus target description"
type="target"
`, port),
		CapabilityRegistry: capabilitiesRegistry,
	}))

	// Create trigger
	_, err := capabilitiesServer.MockRegistry.CreateCapability(ctx, &pb.CapabilityInfo{
		ID:             "some-other-target@1.0.0",
		CapabilityType: 4,
		Description:    "bogus other target description",
		DON:            nil,
		IsLocal:        true,
	})
	require.NoError(t, err)

	capabilitiesInfos, err := capabilitiesServer.Infos(ctx)
	require.NoError(t, err)
	require.Len(t, capabilitiesInfos, 2)

	require.ElementsMatch(t, capabilitiesInfos, []capabilities.CapabilityInfo{
		{
			ID:             "some-target@1.0.0",
			CapabilityType: "target",
			Description:    "bogus target description",
			DON:            nil,
			IsLocal:        true,
		},
		{
			ID:             "some-other-target@1.0.0",
			CapabilityType: "target",
			Description:    "bogus other target description",
			DON:            nil,
			IsLocal:        true,
		},
	})

	require.NoError(t, capabilitiesRegistry.Contains([]string{"some-target@1.0.0"}))
	require.NoError(t, capabilitiesRegistry.Contains([]string{"some-other-target@1.0.0"}))

	// Register to target
	r1, err := capabilitiesRegistry.GetTarget(ctx, "some-target@1.0.0")
	require.NoError(t, err)
	err = r1.RegisterToWorkflow(ctx, capabilities.RegisterToWorkflowRequest{
		Metadata: capabilities.RegistrationMetadata{},
		Config:   nil,
	})
	require.NoError(t, err)

	r2, err := capabilitiesRegistry.GetTarget(ctx, "some-other-target@1.0.0")
	require.NoError(t, err)
	err = r2.RegisterToWorkflow(ctx, capabilities.RegisterToWorkflowRequest{
		Metadata: capabilities.RegistrationMetadata{},
		Config:   nil,
	})
	require.NoError(t, err)

	// Connect to grpc server
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	client := pb.NewMockCapabilityClient(conn)

	var wg sync.WaitGroup
	hookExecutables, err := client.HookExecutables(context.Background())
	require.NoError(t, err)

	ch := make(chan *pb.ExecutableRequest, 100)

	var reqCount, resCount int

	wg.Add(1)
	go func() {
		wg.Done()
		for {
			m, err2 := hookExecutables.Recv()
			require.NoError(t, err2)
			logger.Infow("Got execute, forwarding request", "m", m.String())
			ch <- m
			reqCount++
			if reqCount > 1 {
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		wg.Done()
		for {
			m := <-ch
			logger.Infow("Got message, forwarding response", "m", m.String())
			err2 := hookExecutables.Send(&pb.ExecutableResponse{
				ID:             m.ID,
				CapabilityType: m.CapabilityType,
				Value:          nil,
			})
			require.NoError(t, err2)
			resCount++
			if resCount > 1 {
				return
			}
		}
	}()

	_, err = r1.Execute(ctx, capabilities.CapabilityRequest{
		Metadata: capabilities.RequestMetadata{},
		Config:   nil,
		Inputs:   nil,
	})
	require.NoError(t, err)
	_, err = r2.Execute(ctx, capabilities.CapabilityRequest{
		Metadata: capabilities.RequestMetadata{},
		Config:   nil,
		Inputs:   nil,
	})
	require.NoError(t, err)
	wg.Wait()
}

func connectWithRetry(port int, maxAttempts int) (pb.MockCapabilityClient, error) {
	address := fmt.Sprintf("127.0.0.1:%d", port)
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		conn, err := grpc.NewClient(
			address,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err == nil {
			return pb.NewMockCapabilityClient(conn), nil
		}
		lastErr = err

		if attempt < maxAttempts {
			time.Sleep(100 * time.Millisecond)
			continue
		}
	}
	return nil, fmt.Errorf("failed to connect after %d attempts: %w", maxAttempts, lastErr)
}

func TestMockServer_Initialise_IncompleteData(t *testing.T) {
	t.Parallel()
	logger := testutils.NewLogger(t)
	capabilitiesRegistry := testutils.NewCapabilitiesRegistry(t)
	ctx := context.Background()

	// Test case 1: Empty config
	t.Run("empty config", func(t *testing.T) {
		server := New(logger)
		err := server.Initialise(ctx, core.StandardCapabilitiesDependencies{
			CapabilityRegistry: capabilitiesRegistry,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing config")
	})

	// Test case 2: Config with no port
	t.Run("missing port", func(t *testing.T) {
		server := New(logger)
		err := server.Initialise(ctx, core.StandardCapabilitiesDependencies{
			Config: `
			[[DefaultMocks]]
			id="some-trigger@1.0.0"
			description="test trigger"
			type="trigger"
		`,
			CapabilityRegistry: capabilitiesRegistry,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "must specify a port number")
	})

	// Test case 3: Config with port but no default mocks (valid but empty)
	t.Run("no default mocks", func(t *testing.T) {
		server := New(logger)
		err := server.Initialise(ctx, core.StandardCapabilitiesDependencies{
			Config: `
			port=9999
		`,
			CapabilityRegistry: capabilitiesRegistry,
		})
		require.NoError(t, err)
		require.Empty(t, server.MockRegistry.Triggers)
		require.Empty(t, server.MockRegistry.Executables)
	})

	// Test case 4: Config with invalid TOML syntax
	t.Run("invalid TOML", func(t *testing.T) {
		server := New(logger)
		err := server.Initialise(ctx, core.StandardCapabilitiesDependencies{
			Config: `
			port=9999
			[[DefaultMocks] # Missing closing bracket
			id="some-trigger@1.0.0"
			description="test trigger"
			type="trigger"
		`,
			CapabilityRegistry: capabilitiesRegistry,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to unmarshal config")
	})

	// Test case 5: Config with missing fields in default mocks
	t.Run("missing type field", func(t *testing.T) {
		server := New(logger)
		err := server.Initialise(ctx, core.StandardCapabilitiesDependencies{
			Config: `
			port=9999
			[[DefaultMocks]]
			id="some-trigger@1.0.0"
			description="test trigger"
			# type field missing
		`,
			CapabilityRegistry: capabilitiesRegistry,
		})
		require.Contains(t, err.Error(), "capability type not supported")
		require.Empty(t, server.MockRegistry.Triggers)
		require.Empty(t, server.MockRegistry.Executables)
	})

	// Test case 6: Config with invalid capability type
	t.Run("invalid capability type", func(t *testing.T) {
		server := New(logger)
		err := server.Initialise(ctx, core.StandardCapabilitiesDependencies{
			Config: `
			port=9999
			[[DefaultMocks]]
			id="some-trigger@1.0.0"
			description="test trigger"
			type="invalid-type"
		`,
			CapabilityRegistry: capabilitiesRegistry,
		})
		require.Contains(t, err.Error(), "capability type not supported")
		require.Empty(t, server.MockRegistry.Triggers)
		require.Empty(t, server.MockRegistry.Executables)
	})
}
