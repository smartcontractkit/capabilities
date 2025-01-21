package actions_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/smartcontractkit/capabilities/libs/testutils"

	actions "github.com/smartcontractkit/capabilities/readcontract/action"
	"github.com/smartcontractkit/capabilities/readcontract/readcontractcap"
)

func TestReadContractAction_Execute_WithoutConsensus(t *testing.T) {
	testReadContractActionExecute(t)
}

func testReadContractActionExecute(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		ctx := tests.Context(t)

		config := actions.ReadContractConfig{
			ChainID:           1,
			Network:           "testnet",
			SupportsConsensus: true,
		}

		relayerMock := NewRelayer(t)
		returnVal, err := values.Wrap(5)
		require.NoError(t, err)
		contractReaderMock := &mockContractReader{returnVal: returnVal}

		// Set up the relayer mock to return the contract reader mock
		relayerMock.On("NewContractReader", mock.Anything, mock.Anything).Return(contractReaderMock, nil)

		lggr := logger.Test(t)

		oracleFactory := testutils.NewOracleFactory(t, lggr)

		action, err := actions.NewReadContractAction(ctx, lggr, config, relayerMock, oracleFactory, clockwork.NewRealClock())
		require.NoError(t, err)

		servicetest.Run(t, action)

		capconfig := readcontractcap.Config{
			ContractReaderConfig: "some-config",
			ReadIdentifier:       "TestReadIdentifier",
			ContractAddress:      "0x123",
			ContractName:         "TestContract",
		}
		configAsValueMap, err := values.WrapMap(capconfig)
		require.NoError(t, err)

		err = action.RegisterToWorkflow(ctx, capabilities.RegisterToWorkflowRequest{
			Metadata: capabilities.RegistrationMetadata{
				WorkflowID:    "workflowID",
				WorkflowOwner: "",
			},
			Config: configAsValueMap,
		})
		require.NoError(t, err)

		inputs := readcontractcap.Input{
			ConfidenceLevel: "finalized",
			Params: readcontractcap.InputParams{
				"param1": "value1",
				"param2": "value2",
			},
		}

		requestInputs, err := values.WrapMap(inputs)
		assert.NoError(t, err)

		request := capabilities.CapabilityRequest{
			Config: configAsValueMap,
			Inputs: requestInputs,
			Metadata: capabilities.RequestMetadata{
				WorkflowID: "workflowID",
			},
		}

		response, err := action.Execute(ctx, request)

		require.NoError(t, err)
		assert.NotNil(t, response)

		output := actions.Output{}
		err = response.Value.UnwrapTo(&output)
		require.NoError(t, err)

		var result int
		err = output.LatestValue.UnwrapTo(&result)
		require.NoError(t, err)

		assert.Equal(t, 5, result)
		relayerMock.AssertExpectations(t)
		assert.Equal(t, 1, contractReaderMock.bindingsCallCount)
		assert.Equal(t, 1, contractReaderMock.getLatestValueCallCount)

		// Check that the params are passed as expected
		expectedParams := readcontractcap.InputParams{
			"param1": "value1",
			"param2": "value2",
		}

		assert.Equal(t, expectedParams, contractReaderMock.receivedParams[0])

		// Unregister from workflow
		err = action.UnregisterFromWorkflow(ctx, capabilities.UnregisterFromWorkflowRequest{
			Metadata: capabilities.RegistrationMetadata{
				WorkflowID:    "workflowID",
				WorkflowOwner: "",
			},
			Config: configAsValueMap,
		})
		require.NoError(t, err)
	})

	t.Run("contract reader returns an error", func(t *testing.T) {
		ctx := tests.Context(t)

		config := actions.ReadContractConfig{
			ChainID:           1,
			Network:           "testnet",
			SupportsConsensus: true,
		}

		relayerMock := NewRelayer(t)
		returnVal, err := values.Wrap(5)
		require.NoError(t, err)
		contractReaderMock := &mockContractReader{returnVal: returnVal, returnErr: fmt.Errorf("some error")}

		// Set up the relayer mock to return the contract reader mock
		relayerMock.On("NewContractReader", mock.Anything, mock.Anything).Return(contractReaderMock, nil)

		lggr := logger.Test(t)

		oracleFactory := testutils.NewOracleFactory(t, lggr)

		action, err := actions.NewReadContractAction(ctx, lggr, config, relayerMock, oracleFactory, clockwork.NewRealClock())
		require.NoError(t, err)

		servicetest.Run(t, action)

		capconfig := readcontractcap.Config{
			ContractReaderConfig: "some-config",
			ReadIdentifier:       "TestReadIdentifier",
			ContractAddress:      "0x123",
			ContractName:         "TestContract",
		}
		configAsValueMap, err := values.WrapMap(capconfig)
		require.NoError(t, err)

		err = action.RegisterToWorkflow(ctx, capabilities.RegisterToWorkflowRequest{
			Metadata: capabilities.RegistrationMetadata{
				WorkflowID:    "workflowID",
				WorkflowOwner: "",
			},
			Config: configAsValueMap,
		})
		require.NoError(t, err)

		inputs := readcontractcap.Input{
			ConfidenceLevel: "finalized",
			Params: readcontractcap.InputParams{
				"param1": "value1",
				"param2": "value2",
			},
		}

		requestInputs, err := values.WrapMap(inputs)
		assert.NoError(t, err)

		request := capabilities.CapabilityRequest{
			Config: configAsValueMap,
			Inputs: requestInputs,
			Metadata: capabilities.RequestMetadata{
				WorkflowID: "workflowID",
			},
		}

		_, err = action.Execute(ctx, request)
		require.ErrorIs(t, err, contractReaderMock.returnErr)
	})
}

type mockContractReader struct {
	returnVal values.Value
	returnErr error

	receivedParams          []any
	getLatestValueCallCount int
	bindingsCallCount       int
	allBindings             [][]types.BoundContract
}

func (m *mockContractReader) GetLatestValueWithHeadData(ctx context.Context, readIdentifier string, confidenceLevel primitives.ConfidenceLevel, params, returnVal any) (*types.Head, error) {
	m.getLatestValueCallCount++
	m.receivedParams = append(m.receivedParams, params)

	ptrToValue := returnVal.(*values.Value)
	*ptrToValue = m.returnVal

	return &types.Head{
		Height:    "1",
		Hash:      nil,
		Timestamp: 0,
	}, m.returnErr
}

func (m *mockContractReader) Bind(ctx context.Context, bindings []types.BoundContract) error {
	m.bindingsCallCount++
	m.allBindings = append(m.allBindings, bindings)
	return nil
}

func (m *mockContractReader) Start(ctx context.Context) error {
	return nil
}

func (m *mockContractReader) Close() error {
	return nil
}

func (m *mockContractReader) Ready() error {
	return nil
}

func (m *mockContractReader) HealthReport() map[string]error {
	return nil
}

func (m *mockContractReader) Name() string {
	return "mockContractReader"
}

func TestReadContractStore_Get(t *testing.T) {
	store := actions.NewReadContractStore()

	metadata := capabilities.RegistrationMetadata{
		WorkflowID:  "workflowID",
		ReferenceID: "stepReference",
	}

	reader := &mockCapabilityContractReader{}

	// Test Add
	store.Add(metadata, reader)
	retrievedReader, exists := store.Get(capabilities.RequestMetadata{
		WorkflowID:  "workflowID",
		ReferenceID: "stepReference",
	})
	require.True(t, exists)
	assert.Equal(t, reader, retrievedReader)

	// Test Get
	retrievedReader, exists = store.Get(capabilities.RequestMetadata{
		WorkflowID:  "workflowID",
		ReferenceID: "stepReference",
	})
	require.True(t, exists)
	assert.Equal(t, reader, retrievedReader)

	// Test Remove
	store.Remove(metadata)
	_, exists = store.Get(capabilities.RequestMetadata{
		WorkflowID:  "workflowID",
		ReferenceID: "stepReference",
	})
	require.False(t, exists)
}

type mockCapabilityContractReader struct {
	mock.Mock
}

func (m *mockCapabilityContractReader) GetLatestValue(ctx context.Context, requestID string, confidenceLevel primitives.ConfidenceLevel, params any) (values.Value, error) {
	args := m.Called(ctx, requestID, confidenceLevel, params)
	return args.Get(0).(values.Value), args.Error(1)
}
