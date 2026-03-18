package actions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	typesmocks "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
)

func TestView_Success(t *testing.T) {
	t.Parallel()

	svc := typesmocks.NewAptosService(t)
	svc.On("View", mock.Anything, mock.AnythingOfType("aptos.ViewRequest")).
		Run(func(args mock.Arguments) {
			req := args.Get(1).(aptostypes.ViewRequest)
			require.NotNil(t, req.Payload)
			require.Equal(t, "name", req.Payload.Function)
		}).
		Return(&aptostypes.ViewReply{Data: []byte(`["Aptos Coin"]`)}, nil).
		Once()

	a := &Aptos{AptosService: svc}
	meta := capabilities.RequestMetadata{
		WorkflowExecutionID: "weid",
		ReferenceID:         "step-id",
	}
	input := &aptoscap.ViewRequest{
		Payload: &aptoscap.ViewPayload{
			Module: &aptoscap.ModuleID{
				Address: []byte{1},
				Name:    "coin",
			},
			Function: "name",
		},
	}

	resp, err := a.View(context.Background(), meta, input)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, []byte(`["Aptos Coin"]`), resp.Response.Data)
}

func TestView_ValidationErrors(t *testing.T) {
	t.Parallel()

	a := &Aptos{AptosService: typesmocks.NewAptosService(t)}
	meta := capabilities.RequestMetadata{
		WorkflowExecutionID: "weid",
		ReferenceID:         "step-id",
	}

	_, err := a.View(context.Background(), meta, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil ViewRequest")

	_, err = a.View(context.Background(), meta, &aptoscap.ViewRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ViewRequest.Payload is required")
}
