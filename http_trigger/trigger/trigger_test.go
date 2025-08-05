package trigger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	gcmocks "github.com/smartcontractkit/chainlink-common/pkg/types/core/mocks"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

func TestService_RegisterTrigger(t *testing.T) {
	type testCase struct {
		name                string
		sendChannelBufSize  uint16
		registerErr         error
		expectedChanBufSize uint16
		expectErr           bool
	}
	tests := []testCase{
		{
			name:                "success with default buffer size",
			sendChannelBufSize:  0,
			registerErr:         nil,
			expectedChanBufSize: defaultSendChannelBufferSize,
			expectErr:           false,
		},
		{
			name:                "success with custom buffer size",
			sendChannelBufSize:  42,
			registerErr:         nil,
			expectedChanBufSize: 42,
			expectErr:           false,
		},
		{
			name:                "error from RegisterWorkflow",
			sendChannelBufSize:  0,
			registerErr:         errors.New("register error"),
			expectedChanBufSize: defaultSendChannelBufferSize,
			expectErr:           true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockHandler := &mockConnectorHandler{
				registerErr: tc.registerErr,
			}
			svc := NewService(logger.Test(t))
			cfgStr := fmt.Sprintf(`{"sendChannelBufferSize": %d}`, tc.sendChannelBufSize)
			gc := mockedGatewayConnector(t)
			err := svc.Initialise(t.Context(), cfgStr, nil, nil, nil, nil, nil, nil, gc, nil)
			require.NoError(t, err)
			svc.connectorHandler = mockHandler
			ctx := context.Background()
			meta := capabilities.RequestMetadata{WorkflowID: "abcdef", WorkflowOwner: "123456", WorkflowName: "456789", WorkflowTag: "tag"}
			input := &http.Config{}

			ch, err := svc.RegisterTrigger(ctx, "tid", meta, input)
			if tc.expectErr {
				require.Error(t, err)
				require.Nil(t, ch)
			} else {
				require.Equal(t, tc.expectedChanBufSize, uint16(cap(ch))) //nolint:gosec // G115
				require.Equal(t, strings.ToLower(ensureHexPrefix(meta.WorkflowID)), mockHandler.lastWorkflowSelector.WorkflowID)
				require.Equal(t, strings.ToLower(ensureHexPrefix(meta.WorkflowOwner)), mockHandler.lastWorkflowSelector.WorkflowOwner)
				require.Equal(t, strings.ToLower(ensureHexPrefix(meta.WorkflowName)), mockHandler.lastWorkflowSelector.WorkflowName)
				require.Equal(t, meta.WorkflowTag, mockHandler.lastWorkflowSelector.WorkflowTag)
				require.Equal(t, input, mockHandler.lastInput)
			}
		})
	}
}

func TestService_UnregisterTrigger(t *testing.T) {
	tests := []struct {
		name       string
		handlerErr error
	}{
		{
			name:       "successfully unregisters workflow",
			handlerErr: nil,
		},
		{
			name:       "logs error if handler fails",
			handlerErr: fmt.Errorf("some error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHandler := &mockConnectorHandler{
				unregisterErr: tt.handlerErr,
			}
			svc := NewService(logger.Test(t))
			cfg := "{}"
			gc := mockedGatewayConnector(t)
			err := svc.Initialise(t.Context(), cfg, nil, nil, nil, nil, nil, nil, gc, nil)
			require.NoError(t, err)
			svc.connectorHandler = mockHandler

			metadata := capabilities.RequestMetadata{WorkflowID: "wid-123"}
			err = svc.UnregisterTrigger(context.Background(), "tid-1", metadata, nil)
			if tt.handlerErr != nil {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.handlerErr.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestService_Start_HealthReport_Ready_Close(t *testing.T) {
	mockHandler := &mockConnectorHandler{}
	svc := NewService(logger.Test(t))
	cfg := "{}"
	gc := mockedGatewayConnector(t)
	err := svc.Initialise(t.Context(), cfg, nil, nil, nil, nil, nil, nil, gc, nil)
	require.NoError(t, err)
	svc.connectorHandler = mockHandler

	ctx := context.Background()

	// HealthReport should report healthy
	hr := svc.HealthReport()
	require.Contains(t, hr, svc.Name())
	require.NoError(t, hr[svc.Name()])
	require.NoError(t, svc.Ready())

	// Restarting the service should return an error
	require.Error(t, svc.Start(ctx))

	// Close the service
	err = svc.Close()
	require.NoError(t, err)
	hr = svc.HealthReport()
	require.Contains(t, hr, svc.Name())
	require.Error(t, hr[svc.Name()])
	require.Error(t, svc.Ready())
}

// mockConnectorHandler implements minimal RegisterWorkflow/UnregisterWorkflow for testing
type mockConnectorHandler struct {
	registerErr          error
	unregisterErr        error
	lastWorkflowSelector gateway_common.WorkflowSelector
	lastInput            *http.Config
}

func (m *mockConnectorHandler) RegisterWorkflow(ctx context.Context, workflowSelector gateway_common.WorkflowSelector, input *http.Config, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error {
	m.lastWorkflowSelector = workflowSelector
	m.lastInput = input
	return m.registerErr
}
func (m *mockConnectorHandler) UnregisterWorkflow(ctx context.Context, workflowID string) error {
	return m.unregisterErr
}
func (m *mockConnectorHandler) Start(ctx context.Context) error { return nil }
func (m *mockConnectorHandler) Close() error                    { return nil }
func (m *mockConnectorHandler) HealthReport() map[string]error {
	return map[string]error{"mockConnectorHandler": nil}
}
func (m *mockConnectorHandler) Name() string {
	return "mockConnectorHandler"
}
func (m *mockConnectorHandler) Ready() error {
	return nil
}

func mockedGatewayConnector(t *testing.T) *gcmocks.GatewayConnector {
	gc := gcmocks.NewGatewayConnector(t)
	gc.EXPECT().AddHandler(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	return gc
}
