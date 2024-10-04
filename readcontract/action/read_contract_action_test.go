package actions_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	actions "github.com/smartcontractkit/capabilities/readcontract/action"
	"github.com/smartcontractkit/capabilities/readcontract/readcontractcap"
)

func TestReadContractAction_Execute(t *testing.T) {
	config := actions.ReadContractConfig{
		ChainID: 1,
		Network: "testnet",
	}

	relayerMock := NewRelayer(t)
	returnVal, err := values.Wrap(5)
	assert.NoError(t, err)
	contractReaderMock := &mockContractReader{returnVal: returnVal}

	// Set up the relayer mock to return the contract reader mock
	relayerMock.On("NewContractReader", mock.Anything, mock.Anything).Return(contractReaderMock, nil)

	action, err := setupReadContractAction(t, config, relayerMock)
	assert.NoError(t, err)

	inputs := readcontractcap.Input{
		ReadIdentifier:  "TestReadIdentifier",
		Address:         "0x123",
		ConfidenceLevel: "finalized",
		Params: readcontractcap.InputParams{
			"param1": "value1",
			"param2": "value2",
		},
	}

	request := createCapabilityRequest(t, "some-config", inputs)

	response, err := action.Execute(context.Background(), request)

	assert.NoError(t, err)
	assert.NotNil(t, response)

	output := actions.Output{}
	err = response.Value.UnwrapTo(&output)
	assert.NoError(t, err)

	var result int
	err = output.LatestValue.UnwrapTo(&result)
	assert.NoError(t, err)

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
}

func TestReadContractAction_ExecuteMultipleTimeWithSameReaderConfigUsesSingleInstance(t *testing.T) {
	config := actions.ReadContractConfig{
		ChainID: 1,
		Network: "testnet",
	}

	relayerMock := NewRelayer(t)
	returnVal, err := values.Wrap(5)
	assert.NoError(t, err)
	contractReaderMock := &mockContractReader{returnVal: returnVal}

	// Set up the relayer mock to return the contract reader mock
	relayerMock.On("NewContractReader", mock.Anything, mock.Anything).Return(contractReaderMock, nil).Once()

	action, err := setupReadContractAction(t, config, relayerMock)
	assert.NoError(t, err)

	inputs := readcontractcap.Input{
		ReadIdentifier:  "TestReadIdentifier",
		Address:         "0x123",
		ConfidenceLevel: "finalized",
		Params: readcontractcap.InputParams{
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

		output := actions.Output{}
		val, err := values.Wrap(5)
		assert.NoError(t, err)
		output.LatestValue = val

		expectedResult, err := values.WrapMap(output)
		assert.NoError(t, err)

		assert.Equal(t, expectedResult, response.Value)
	}

	relayerMock.AssertExpectations(t)
	assert.Equal(t, 3, contractReaderMock.bindingsCallCount)
	assert.Equal(t, 3, contractReaderMock.getLatestValueCallCount)
}

func TestReadContractAction_ExecuteWithDifferentReaderConfigUsesDifferentContractReaderInstances(t *testing.T) {
	config := actions.ReadContractConfig{
		ChainID: 1,
		Network: "testnet",
	}

	relayerMock := NewRelayer(t)
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

	action, err := setupReadContractAction(t, config, relayerMock)
	assert.NoError(t, err)

	inputs := readcontractcap.Input{
		ReadIdentifier:  "TestReadIdentifier",
		Address:         "0x123",
		ConfidenceLevel: "finalized",
		Params: readcontractcap.InputParams{
			"param1": "value1",
			"param2": "value2",
		},
	}

	// First request with config "some-config-1"
	request1 := createCapabilityRequest(t, "some-config-1", inputs)

	response1, err := action.Execute(context.Background(), request1)
	assert.NoError(t, err)
	assert.NotNil(t, response1)

	val1, err := values.Wrap(5)
	assert.NoError(t, err)
	result1 := actions.Output{LatestValue: val1}

	expectedResult1, err := values.WrapMap(result1)
	assert.NoError(t, err)

	assert.Equal(t, expectedResult1, response1.Value)

	// Second request with config "some-config-2"
	request2 := createCapabilityRequest(t, "some-config-2", inputs)

	response2, err := action.Execute(context.Background(), request2)
	assert.NoError(t, err)
	assert.NotNil(t, response2)

	val2, err := values.Wrap(10)
	assert.NoError(t, err)
	result2 := actions.Output{LatestValue: val2}

	expectedResult2, err := values.WrapMap(result2)
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
		ChainID: 1,
		Network: "testnet",
	}

	relayerMock := NewRelayer(t)
	returnVal, err := values.Wrap(5)
	assert.NoError(t, err)
	contractReaderMock := &mockContractReader{returnVal: returnVal}

	// Set up the relayer mock to return the contract reader mock
	relayerMock.On("NewContractReader", mock.Anything, mock.Anything).Return(contractReaderMock, nil).Once()

	action, err := setupReadContractAction(t, config, relayerMock)
	assert.NoError(t, err)

	inputs1 := readcontractcap.Input{
		ReadIdentifier:  "TestReadIdentifier",
		Address:         "0x123",
		ConfidenceLevel: "finalized",
		Params: readcontractcap.InputParams{
			"param1": "value1",
			"param2": "value2",
		},
	}

	inputs2 := readcontractcap.Input{
		ReadIdentifier:  "TestReadIdentifier",
		Address:         "0x456",
		ConfidenceLevel: "finalized",
		Params: readcontractcap.InputParams{
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

	val1, err := values.Wrap(5)
	assert.NoError(t, err)
	result1 := actions.Output{LatestValue: val1}

	expectedResult1, err := values.WrapMap(result1)
	assert.NoError(t, err)

	assert.Equal(t, expectedResult1, response1.Value)

	// Second request
	response2, err := action.Execute(context.Background(), request2)
	assert.NoError(t, err)
	assert.NotNil(t, response2)

	val2, err := values.Wrap(5)
	assert.NoError(t, err)
	result2 := actions.Output{LatestValue: val2}

	expectedResult2, err := values.WrapMap(result2)
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

func setupReadContractAction(t *testing.T, config actions.ReadContractConfig, relayerMock *Relayer) (*actions.ReadContractAction, error) {
	lggr := logger.Test(t)
	return actions.NewReadContractAction(lggr, config, relayerMock)
}

func createCapabilityRequest(t *testing.T, contractReaderConfig string, inputs readcontractcap.Input) capabilities.CapabilityRequest {
	config := readcontractcap.Config{ContractReaderConfig: contractReaderConfig}
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
