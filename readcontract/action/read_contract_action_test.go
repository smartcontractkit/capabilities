package actions_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	actions "github.com/smartcontractkit/capabilities/readcontract/action"
	"github.com/smartcontractkit/capabilities/readcontract/action/mocks"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

func TestReadContractAction_Execute(t *testing.T) {
	config := actions.ReadContractConfig{
		ChainId: 1,
		Network: "testnet",
	}

	relayerMock := &mocks.Relayer{}
	returnVal, err := values.Wrap(5)
	assert.NoError(t, err)
	contractReaderMock := &mockContractReader{returnVal: returnVal}

	// Set up the relayer mock to return the contract reader mock
	relayerMock.On("NewContractReader", mock.Anything, mock.Anything).Return(contractReaderMock, nil)

	action := setupReadContractAction(t, config, relayerMock)

	inputs := map[string]any{
		"readIdentifier":  "TestReadIdentifier",
		"address":         "0x123",
		"confidenceLevel": "finalized",
		"params": map[string]any{
			"param1": "value1",
			"param2": "value2",
		},
	}

	request := createCapabilityRequest(t, "some-config", inputs)

	response, err := action.Execute(context.Background(), request)

	assert.NoError(t, err)
	assert.NotNil(t, response)

	resultMap := map[string]any{}
	resultMap["latestValue"] = 5

	latestValue := response.Value.Underlying["latestValue"]
	var result int
	err = latestValue.UnwrapTo(&result)
	assert.NoError(t, err)

	assert.Equal(t, 5, result)
	relayerMock.AssertExpectations(t)
	assert.Equal(t, 1, contractReaderMock.bindingsCallCount)
	assert.Equal(t, 1, contractReaderMock.getLatestValueCallCount)

	// Check that the params are passed as expected
	expectedParams := map[string]any{
		"param1": "value1",
		"param2": "value2",
	}
	assert.Equal(t, expectedParams, contractReaderMock.receivedParams[0])
}

func TestReadContractAction_ExecuteMultipleTimeWithSameReaderConfigUsesSingleInstance(t *testing.T) {
	config := actions.ReadContractConfig{
		ChainId: 1,
		Network: "testnet",
	}

	relayerMock := &mocks.Relayer{}
	returnVal, err := values.Wrap(5)
	assert.NoError(t, err)
	contractReaderMock := &mockContractReader{returnVal: returnVal}

	// Set up the relayer mock to return the contract reader mock
	relayerMock.On("NewContractReader", mock.Anything, mock.Anything).Return(contractReaderMock, nil).Once()

	action := setupReadContractAction(t, config, relayerMock)

	inputs := map[string]any{
		"readIdentifier":  "TestReadIdentifier",
		"address":         "0x123",
		"confidenceLevel": "finalized",
		"params": map[string]any{
			"param1": "value1",
			"param2": "value2",
		},
	}

	request := createCapabilityRequest(t, "some-config", inputs)

	// Call Execute multiple times
	for i := 0; i < 3; i++ {
		response, err := action.Execute(context.Background(), request)
		assert.NoError(t, err)
		assert.NotNil(t, response)

		resultMap := map[string]any{}
		resultMap["latestValue"] = 5

		expectedResult, err := values.WrapMap(resultMap)
		assert.NoError(t, err)

		assert.Equal(t, expectedResult, response.Value)
	}

	relayerMock.AssertExpectations(t)
	assert.Equal(t, 3, contractReaderMock.bindingsCallCount)
	assert.Equal(t, 3, contractReaderMock.getLatestValueCallCount)
}

func TestReadContractAction_ExecuteWithDifferentReaderConfigUsesDifferentContractReaderInstances(t *testing.T) {
	config := actions.ReadContractConfig{
		ChainId: 1,
		Network: "testnet",
	}

	relayerMock := &mocks.Relayer{}
	returnVal1, err := values.Wrap(5)
	assert.NoError(t, err)
	contractReaderMock1 := &mockContractReader{returnVal: returnVal1}

	returnVal2, err := values.Wrap(10)
	assert.NoError(t, err)
	contractReaderMock2 := &mockContractReader{returnVal: returnVal2}

	// Set up the relayer mock to return different contract reader mocks for different configs
	relayerMock.On("NewContractReader", mock.Anything, mock.MatchedBy(func(config []byte) bool {
		return string(config) == `"some-config-1"`
	})).Return(contractReaderMock1, nil).Once()

	relayerMock.On("NewContractReader", mock.Anything, mock.MatchedBy(func(config []byte) bool {
		return string(config) == `"some-config-2"`
	})).Return(contractReaderMock2, nil).Once()

	action := setupReadContractAction(t, config, relayerMock)

	inputs := map[string]any{
		"readIdentifier":  "TestReadIdentifier",
		"address":         "0x123",
		"confidenceLevel": "finalized",
		"params": map[string]any{
			"param1": "value1",
			"param2": "value2",
		},
	}

	// First request with config "some-config-1"
	request1 := createCapabilityRequest(t, "some-config-1", inputs)

	response1, err := action.Execute(context.Background(), request1)
	assert.NoError(t, err)
	assert.NotNil(t, response1)

	resultMap1 := map[string]any{}
	resultMap1["latestValue"] = 5

	expectedResult1, err := values.WrapMap(resultMap1)
	assert.NoError(t, err)

	assert.Equal(t, expectedResult1, response1.Value)

	// Second request with config "some-config-2"
	request2 := createCapabilityRequest(t, "some-config-2", inputs)

	response2, err := action.Execute(context.Background(), request2)
	assert.NoError(t, err)
	assert.NotNil(t, response2)

	resultMap2 := map[string]any{}
	resultMap2["latestValue"] = 10

	expectedResult2, err := values.WrapMap(resultMap2)
	assert.NoError(t, err)

	assert.Equal(t, expectedResult2, response2.Value)

	relayerMock.AssertExpectations(t)
	assert.Equal(t, 1, contractReaderMock1.bindingsCallCount)
	assert.Equal(t, 1, contractReaderMock1.getLatestValueCallCount)
	assert.Equal(t, 1, contractReaderMock2.bindingsCallCount)
	assert.Equal(t, 1, contractReaderMock2.getLatestValueCallCount)
}

func TestReadContractAction_ExecuteSameContractDifferentAddresses(t *testing.T) {
	config := actions.ReadContractConfig{
		ChainId: 1,
		Network: "testnet",
	}

	relayerMock := &mocks.Relayer{}
	returnVal, err := values.Wrap(5)
	assert.NoError(t, err)
	contractReaderMock := &mockContractReader{returnVal: returnVal}

	// Set up the relayer mock to return the contract reader mock
	relayerMock.On("NewContractReader", mock.Anything, mock.Anything).Return(contractReaderMock, nil).Once()

	action := setupReadContractAction(t, config, relayerMock)

	inputs1 := map[string]any{
		"readIdentifier":  "TestReadIdentifier",
		"address":         "0x123",
		"confidenceLevel": "finalized",
		"params": map[string]any{
			"param1": "value1",
			"param2": "value2",
		},
	}

	inputs2 := map[string]any{
		"readIdentifier":  "TestReadIdentifier",
		"address":         "0x456",
		"confidenceLevel": "finalized",
		"params": map[string]any{
			"param1": "value1",
			"param2": "value2",
		},
	}

	request1 := createCapabilityRequest(t, "some-config", inputs1)
	request2 := createCapabilityRequest(t, "some-config", inputs2)

	// First request
	response1, err := action.Execute(context.Background(), request1)
	assert.NoError(t, err)
	assert.NotNil(t, response1)

	resultMap1 := map[string]any{}
	resultMap1["latestValue"] = 5

	expectedResult1, err := values.WrapMap(resultMap1)
	assert.NoError(t, err)

	assert.Equal(t, expectedResult1, response1.Value)

	// Second request
	response2, err := action.Execute(context.Background(), request2)
	assert.NoError(t, err)
	assert.NotNil(t, response2)

	resultMap2 := map[string]any{}
	resultMap2["latestValue"] = 5

	expectedResult2, err := values.WrapMap(resultMap2)
	assert.NoError(t, err)

	assert.Equal(t, expectedResult2, response2.Value)

	relayerMock.AssertExpectations(t)
	assert.Equal(t, 2, contractReaderMock.bindingsCallCount)
	assert.Equal(t, 2, contractReaderMock.getLatestValueCallCount)

	// Check allBindings attribute
	expectedBindings := [][]types.BoundContract{
		{{Address: "0x123", Name: "TestReadIdentifier"}},
		{{Address: "0x456", Name: "TestReadIdentifier"}},
	}
	assert.Equal(t, expectedBindings, contractReaderMock.allBindings)
}

func setupReadContractAction(t *testing.T, config actions.ReadContractConfig, relayerMock *mocks.Relayer) *actions.ReadContractAction {
	lggr := logger.Test(t)
	return actions.NewReadContractAction(lggr, config, relayerMock)
}

func createCapabilityRequest(t *testing.T, contractReaderConfig string, inputs map[string]any) capabilities.CapabilityRequest {
	config := map[string]any{}
	config["contractReaderConfig"] = contractReaderConfig
	requestConfig, err := values.WrapMap(config)
	assert.NoError(t, err)

	requestInputs, err := values.WrapMap(inputs)
	assert.NoError(t, err)

	return capabilities.CapabilityRequest{
		Config: requestConfig,
		Inputs: requestInputs,
	}
}

type mockContractReader struct {
	returnVal values.Value

	receivedParams          []any
	getLatestValueCallCount int
	bindingsCallCount       int
	allBindings             [][]types.BoundContract
}

func (m *mockContractReader) GetLatestValue(ctx context.Context, readIdentifier string, confidenceLevel primitives.ConfidenceLevel, params, returnVal any) error {
	m.getLatestValueCallCount++
	m.receivedParams = append(m.receivedParams, params)

	ptrToValue := returnVal.(*values.Value)
	*ptrToValue = m.returnVal

	return nil
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
