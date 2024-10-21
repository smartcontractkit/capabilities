package reportingplugins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/capabilities/readcontract/action/consensus/requests"
)

type mockPendingRequests struct {
	requests []requests.RequestWithConsensusHeight
}

func (m *mockPendingRequests) GetRequestsWithConsensusHeight() []requests.RequestWithConsensusHeight {
	return m.requests
}

type mockValueStore struct {
	values map[string]map[uint64][]byte
}

func (m *mockValueStore) GetValueAtHeight(requestID string, height uint64) []byte {
	return m.values[requestID][height]
}

func TestValueAtHeightReportingPlugin_Query(t *testing.T) {
	pendingRequests := &mockPendingRequests{
		requests: []requests.RequestWithConsensusHeight{
			{RequestID: "req1", Height: 1},
			{RequestID: "req2", Height: 2},
		},
	}
	valueStore := &mockValueStore{
		values: map[string]map[uint64][]byte{
			"req1": {
				1: []byte("value1"),
			},
			"req2": {
				2: []byte("value2"),
			},
		},
	}
	config := ocr3types.ReportingPluginConfig{N: 3, F: 1}
	plugin := NewValueAtHeightReportingPlugin(config, pendingRequests, valueStore)

	query, err := plugin.Query(context.Background(), ocr3types.OutcomeContext{})
	assert.NoError(t, err)

	var requests []requests.RequestWithConsensusHeight
	err = json.Unmarshal(query, &requests)
	assert.NoError(t, err)
	assert.Equal(t, pendingRequests.requests, requests)
}

func TestValueAtHeightReportingPlugin_Observation(t *testing.T) {
	pendingRequests := &mockPendingRequests{
		requests: []requests.RequestWithConsensusHeight{
			{RequestID: "req1", Height: 1},
			{RequestID: "req2", Height: 2},
		},
	}
	valueStore := &mockValueStore{
		values: map[string]map[uint64][]byte{
			"req1": {
				1: []byte("value1"),
			},
			"req2": {
				2: []byte("value2"),
			},
		},
	}
	config := ocr3types.ReportingPluginConfig{N: 3, F: 1}
	plugin := NewValueAtHeightReportingPlugin(config, pendingRequests, valueStore)

	query, _ := json.Marshal(pendingRequests.requests)
	observation, err := plugin.Observation(context.Background(), ocr3types.OutcomeContext{}, query)
	assert.NoError(t, err)

	var observedValues []ObservedValue
	err = json.Unmarshal(observation, &observedValues)
	assert.NoError(t, err)
	assert.Equal(t, []ObservedValue{
		{RequestID: "req1", Value: []byte("value1")},
		{RequestID: "req2", Value: []byte("value2")},
	}, observedValues)
}

func TestValueAtHeightReportingPlugin_ValidateObservation(t *testing.T) {
	pendingRequests := &mockPendingRequests{}
	valueStore := &mockValueStore{}
	config := ocr3types.ReportingPluginConfig{N: 3, F: 1}
	plugin := NewValueAtHeightReportingPlugin(config, pendingRequests, valueStore)

	observations := []ObservedValue{
		{RequestID: "req1", Value: []byte("value1")},
		{RequestID: "req2", Value: []byte("value2")},
	}
	observationBytes, err := json.Marshal(observations)
	assert.NoError(t, err)

	ao := types.AttributedObservation{
		Observation: observationBytes,
	}

	err = plugin.ValidateObservation(context.Background(), ocr3types.OutcomeContext{}, nil, ao)
	assert.NoError(t, err)

	// Test with duplicate id
	observations = append(observations, ObservedValue{RequestID: "req1", Value: []byte("value1")})
	observationBytes, err = json.Marshal(observations)
	assert.NoError(t, err)

	ao.Observation = observationBytes
	err = plugin.ValidateObservation(context.Background(), ocr3types.OutcomeContext{}, nil, ao)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate request ID found")
}

func TestValueAtHeightReportingPlugin_Outcome(t *testing.T) {
	pendingRequests := &mockPendingRequests{}
	valueStore := &mockValueStore{}
	config := ocr3types.ReportingPluginConfig{N: 3, F: 1}
	plugin := NewValueAtHeightReportingPlugin(config, pendingRequests, valueStore)

	// Test case where less than f+1 nodes are in agreement
	observations := []types.AttributedObservation{
		{Observation: []byte(`[{"RequestID":"req1","Value":"` + base64.StdEncoding.EncodeToString([]byte("value1")) + `"}]`)},
		{Observation: []byte(`[{"RequestID":"req1","Value":"` + base64.StdEncoding.EncodeToString([]byte("value2")) + `"}]`)},
	}

	outcome, err := plugin.Outcome(context.Background(), ocr3types.OutcomeContext{}, nil, observations)
	assert.NoError(t, err)

	var observed []ObservedValue
	err = json.Unmarshal(outcome, &observed)
	assert.NoError(t, err)
	assert.Empty(t, observed)

	// Test case where f+1 nodes are in agreement
	observations = []types.AttributedObservation{
		{Observation: []byte(`[{"RequestID":"req1","Value":"` + base64.StdEncoding.EncodeToString([]byte("value1")) + `"}]`)},
		{Observation: []byte(`[{"RequestID":"req1","Value":"` + base64.StdEncoding.EncodeToString([]byte("value1")) + `"}]`)},
		{Observation: []byte(`[{"RequestID":"req1","Value":"` + base64.StdEncoding.EncodeToString([]byte("value2")) + `"}]`)},
	}
	outcome, err = plugin.Outcome(context.Background(), ocr3types.OutcomeContext{}, nil, observations)
	assert.NoError(t, err)

	var observedValues []ObservedValue
	err = json.Unmarshal(outcome, &observedValues)
	assert.NoError(t, err)
	assert.Equal(t, []ObservedValue{
		{RequestID: "req1", Value: []byte("value1")},
	}, observedValues)

	// Test case with multiple Requests and f+1 nodes in agreement
	observations = []types.AttributedObservation{
		{Observation: []byte(`[{"RequestID":"req1","Value":"` + base64.StdEncoding.EncodeToString([]byte("value1")) +
			`"},{"RequestID":"req2","Value":"` + base64.StdEncoding.EncodeToString([]byte("value2")) + `"}]`)},
		{Observation: []byte(`[{"RequestID":"req1","Value":"` + base64.StdEncoding.EncodeToString([]byte("value1")) +
			`"},{"RequestID":"req2","Value":"` + base64.StdEncoding.EncodeToString([]byte("value2")) + `"}]`)},
		{Observation: []byte(`[{"RequestID":"req1","Value":"` + base64.StdEncoding.EncodeToString([]byte("value2")) +
			`"},{"RequestID":"req2","Value":"` + base64.StdEncoding.EncodeToString([]byte("value1")) + `"}]`)},
	}
	outcome, err = plugin.Outcome(context.Background(), ocr3types.OutcomeContext{}, nil, observations)
	assert.NoError(t, err)

	err = json.Unmarshal(outcome, &observedValues)
	assert.NoError(t, err)
	assert.Equal(t, []ObservedValue{
		{RequestID: "req1", Value: []byte("value1")},
		{RequestID: "req2", Value: []byte("value2")},
	}, observedValues)
}

func TestValueAtHeightReportingPlugin_Reports(t *testing.T) {
	pendingRequests := &mockPendingRequests{}
	valueStore := &mockValueStore{}
	config := ocr3types.ReportingPluginConfig{N: 3, F: 1}
	plugin := NewValueAtHeightReportingPlugin(config, pendingRequests, valueStore)

	outcome := []ObservedValue{
		{RequestID: "req1", Value: []byte("value1")},
		{RequestID: "req2", Value: []byte("value2")},
	}
	outcomeBytes, err := json.Marshal(outcome)
	assert.NoError(t, err)

	reports, err := plugin.Reports(context.Background(), 1, outcomeBytes)
	assert.NoError(t, err)

	var reportOutcome []ObservedValue
	err = json.Unmarshal(reports[0].ReportWithInfo.Report, &reportOutcome)
	assert.NoError(t, err)
	assert.Equal(t, outcome, reportOutcome)
}

func TestValueAtHeightReportingPlugin_ShouldTransmitAcceptedReport(t *testing.T) {
	pendingRequests := &mockPendingRequests{}
	valueStore := &mockValueStore{}
	config := ocr3types.ReportingPluginConfig{}
	plugin := NewValueAtHeightReportingPlugin(config, pendingRequests, valueStore)

	// Test case where there are more than 0 observations
	reportWithInfo := ocr3types.ReportWithInfo[[]byte]{
		Report: []byte(`[{"RequestID":"req1","Value":"` + base64.StdEncoding.EncodeToString([]byte("value1")) + `"}]`),
	}
	shouldTransmit, err := plugin.ShouldTransmitAcceptedReport(context.Background(), 1, reportWithInfo)
	assert.NoError(t, err)
	assert.True(t, shouldTransmit)

	// Test case where there are 0 observations
	reportWithInfo = ocr3types.ReportWithInfo[[]byte]{
		Report: []byte(`[]`),
	}
	shouldTransmit, err = plugin.ShouldTransmitAcceptedReport(context.Background(), 1, reportWithInfo)
	assert.NoError(t, err)
	assert.False(t, shouldTransmit)
}
