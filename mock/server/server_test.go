package server

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/consul/sdk/freeport"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/smartcontractkit/capabilities/libs/testutils"

	"github.com/smartcontractkit/capabilities/mock/internal/pb"
	"github.com/smartcontractkit/capabilities/mock/utils"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

func Test_ServerTrigger(t *testing.T) {
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

	require.NoError(t, capabilitiesServer.Initialise(
		ctx,
		fmt.Sprintf(`
port=%d
[[DefaultMocks]]
id="some-trigger@1.0.0"
description="bogus trigger description"
type="trigger"
`, port), // unused - empty config
		nil, // unused - telemetryService core.TelemetryService
		nil, // unused - store core.Store
		capabilitiesRegistry,
		nil, // unused - errorLog core.ErrorLog
		nil, // unused - pipelineRunner core.PipelineRunnerService
		nil, // unused - relayerSet core.RelayerSet
		nil, // unused - oracleFactory core.OracleFactory
	))

	//Create trigger
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

	//Register to trigger
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
	payloadBytes, err := utils.MapToBytes(e)
	require.NoError(t, err)

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

	//Connect to grpc server
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	client := pb.NewMockCapabilityClient(conn)

	_, err = client.SendTriggerEvent(ctx, &pb.SendTriggerEventRequest{
		ID:      "some-trigger@1.0.0",
		EventID: "eventID",
		Payload: payloadBytes,
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

	require.NoError(t, capabilitiesServer.Initialise(
		ctx,
		fmt.Sprintf(`
port=%d
[[DefaultMocks]]
id="some-target@1.0.0"
description="bogus target description"
type="target"
`, port), // unused - empty config
		nil, // unused - telemetryService core.TelemetryService
		nil, // unused - store core.Store
		capabilitiesRegistry,
		nil, // unused - errorLog core.ErrorLog
		nil, // unused - pipelineRunner core.PipelineRunnerService
		nil, // unused - relayerSet core.RelayerSet
		nil, // unused - oracleFactory core.OracleFactory
	))

	//Create trigger
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

	//Register to target
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

	//Connect to grpc server
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
