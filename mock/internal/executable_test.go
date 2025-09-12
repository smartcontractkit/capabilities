package internal

import (
	"context"
	"testing"
	"time"

	"github.com/smartcontractkit/capabilities/mock/internal/pb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/stretchr/testify/require"
)

func TestNewExecutable(t *testing.T) {
	rChan := make(chan ExecutableRequest)
	info := &pb.CapabilityInfo{
		ID:             "test-id",
		CapabilityType: pb.CapabilityType_Target,
		Description:    "test description",
		IsLocal:        true,
	}

	executable := NewExecutable(info, rChan)

	require.Equal(t, "test-id", executable.ID)
	require.Equal(t, capabilities.CapabilityTypeTarget, executable.CapabilityType)
	require.Equal(t, "test description", executable.Description)
	require.True(t, executable.IsLocal)
	require.NotNil(t, executable.ResponseChan)
	require.Equal(t, rChan, executable.requestChan)
}

func TestExecutable_Execute(t *testing.T) {
	rChan := make(chan ExecutableRequest)
	info := &pb.CapabilityInfo{
		ID:             "test-id",
		CapabilityType: pb.CapabilityType_Target,
	}

	executable := NewExecutable(info, rChan)

	t.Run("successful execution", func(t *testing.T) {
		ctx := context.Background()
		request := capabilities.CapabilityRequest{
			Metadata: capabilities.RequestMetadata{
				WorkflowID: "test-workflow",
			},
		}

		v := values.Map{Underlying: map[string]values.Value{
			"payload": values.NewString("test-response"),
		}}
		// Start a goroutine to handle the request and send response
		go func() {
			req := <-rChan
			require.Equal(t, "test-id", req.ID)
			require.Equal(t, pb.CapabilityType_Target, req.CapType)
			require.Equal(t, request, req.Request)

			executable.ResponseChan <- capabilities.CapabilityResponse{
				Value: &v,
			}
		}()

		response, err := executable.Execute(ctx, request)
		require.NoError(t, err)
		require.Equal(t, &v, response.Value)
	})

	t.Run("timeout", func(t *testing.T) {
		ctx := context.Background()
		request := capabilities.CapabilityRequest{}

		// Override timeout for test
		executable.ExecuteTimeout = time.Millisecond * 100
		response, err := executable.Execute(ctx, request)
		require.Error(t, err)
		require.Contains(t, err.Error(), "timeout")
		require.Empty(t, response.Value)
	})
}

func TestExecutable_RegisterToWorkflow(t *testing.T) {
	executable := NewExecutable(&pb.CapabilityInfo{}, make(chan ExecutableRequest))
	err := executable.RegisterToWorkflow(context.Background(), capabilities.RegisterToWorkflowRequest{})
	require.NoError(t, err)
}

func TestExecutable_UnregisterFromWorkflow(t *testing.T) {
	executable := NewExecutable(&pb.CapabilityInfo{}, make(chan ExecutableRequest))
	err := executable.UnregisterFromWorkflow(context.Background(), capabilities.UnregisterFromWorkflowRequest{})
	require.NoError(t, err)
}
