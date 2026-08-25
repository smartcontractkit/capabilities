package trigger

import (
	"context"
	"errors"
	"fmt"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/http/protos"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
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
			svc, err := NewService(logger.Test(t), ServiceConfig{SendChannelBufferSize: tc.sendChannelBufSize}, Dependencies{
				Connector:     mockedGatewayConnector(t),
				Store:         testStore(t),
				LimitsFactory: limits.Factory{Logger: logger.Test(t)},
			})
			require.NoError(t, err)
			svc.connectorHandler = mockHandler
			ctx := context.Background()
			meta := capabilities.RequestMetadata{
				WorkflowID:                    "abcdef",
				WorkflowOwner:                 "123456",
				WorkflowName:                  "456789",
				WorkflowTag:                   "tag",
				WorkflowRegistryChainSelector: "test-chain-selector",
				WorkflowRegistryAddress:       "test-registry-address",
				EngineVersion:                 "1.0.0",
				WorkflowDonID:                 42,
			}
			input := &protos.Config{}

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
				require.Equal(t, meta.WorkflowRegistryChainSelector, mockHandler.lastMetadata.WorkflowRegistryChainSelector)
				require.Equal(t, meta.WorkflowRegistryAddress, mockHandler.lastMetadata.WorkflowRegistryAddress)
				require.Equal(t, meta.EngineVersion, mockHandler.lastMetadata.EngineVersion)
				require.Equal(t, meta.WorkflowDonID, mockHandler.lastMetadata.WorkflowDONID)
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
			svc, err := NewService(logger.Test(t), ServiceConfig{}, Dependencies{
				Connector:     mockedGatewayConnector(t),
				Store:         testStore(t),
				LimitsFactory: limits.Factory{Logger: logger.Test(t)},
			})
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

// TestService_DefaultsConfig covers what a capability told nothing gets: the
// defaults, rather than a service that will not start.
func TestService_DefaultsConfig(t *testing.T) {
	svc, err := NewService(logger.Test(t), ServiceConfig{}, Dependencies{
		Connector:     mockedGatewayConnector(t),
		Store:         testStore(t),
		LimitsFactory: limits.Factory{Logger: logger.Test(t)},
	})
	require.NoError(t, err)

	require.NotNil(t, svc.cfg)
	require.NotNil(t, svc.connectorHandler)
	require.NotNil(t, svc.metrics)
}

func TestService_Start_HealthReport_Ready_Close(t *testing.T) {
	mockHandler := &mockConnectorHandler{}
	svc, err := NewService(logger.Test(t), ServiceConfig{}, Dependencies{
		Connector:     mockedGatewayConnector(t),
		Store:         testStore(t),
		LimitsFactory: limits.Factory{Logger: logger.Test(t)},
	})
	require.NoError(t, err)
	svc.connectorHandler = mockHandler

	ctx := context.Background()

	// Constructed is not started: the process hosting this starts it, which is what
	// makes a capability something that can be built and inspected first.
	require.NoError(t, svc.Start(ctx))

	hr := svc.HealthReport()
	require.Contains(t, hr, svc.Name())
	require.NoError(t, hr[svc.Name()])
	require.NoError(t, svc.Ready())

	// Restarting the service should return an error
	require.Error(t, svc.Start(ctx))

	// Close the service
	require.NoError(t, svc.Close())
	hr = svc.HealthReport()
	require.Contains(t, hr, svc.Name())
	require.Error(t, hr[svc.Name()])
}

// mockConnectorHandler implements minimal RegisterWorkflow/UnregisterWorkflow for testing
type mockConnectorHandler struct {
	registerErr          error
	unregisterErr        error
	lastWorkflowSelector gateway_common.WorkflowSelector
	lastInput            *protos.Config
	lastMetadata         WorkflowRegistrationMetadata
}

func (m *mockConnectorHandler) RegisterWorkflow(ctx context.Context, input WorkflowRegistrationInput, sendCh chan<- capabilities.TriggerAndId[*protos.Payload]) error {
	m.lastWorkflowSelector = input.WorkflowSelector
	m.lastInput = input.Config
	m.lastMetadata = input.Metadata
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

// mockedGatewayConnector is the gateway connection a capability is built with.
//
// AddHandler is Maybe rather than expected: a capability now registers its
// handlers when it starts rather than when it is constructed, and most of these
// tests construct one to look at it rather than to run it.
func mockedGatewayConnector(t *testing.T) *gcmocks.GatewayConnector {
	gc := gcmocks.NewGatewayConnector(t)
	gc.EXPECT().AddHandler(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	return gc
}

// testStore is somewhere to remember answered requests that is not a database.
//
// The capability requires a store rather than defaulting to one, because a
// request cache that a restart empties runs a customer's workflow twice. What it
// requires is the interface, and a test is entitled to a simpler one than
// Postgres.
func testStore(t *testing.T) core.KeyValueStore {
	t.Helper()
	return &mapStore{values: map[string][]byte{}}
}

type mapStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (s *mapStore) Store(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.values[key] = value
	return nil
}

func (s *mapStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.values[key], nil
}

func (s *mapStore) PruneExpiredEntries(context.Context, time.Duration) (int64, error) { return 0, nil }
