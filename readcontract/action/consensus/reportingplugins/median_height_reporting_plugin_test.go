package reportingplugins_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/capabilities/readcontract/action/consensus/reportingplugins"
	"github.com/smartcontractkit/capabilities/readcontract/action/consensus/requests"
)

func TestMedianHeightReportingPlugin_Query(t *testing.T) {
	// Setup
	config := ocr3types.ReportingPluginConfig{}
	requestsStore, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	reqID1 := "reqID1"
	_, err = requestsStore.StartConsensusRequest(ctx, reqID1, 10)
	require.NoError(t, err)

	reqID2 := "reqID2"
	_, err = requestsStore.StartConsensusRequest(ctx, reqID2, 10)
	require.NoError(t, err)

	err = requestsStore.AddObservationForRequest(ctx, reqID1, 100, []byte("value1"))
	require.NoError(t, err)
	err = requestsStore.AddObservationForRequest(ctx, reqID1, 101, []byte("value11"))
	require.NoError(t, err)
	err = requestsStore.AddObservationForRequest(ctx, reqID2, 100, []byte("value1"))
	require.NoError(t, err)

	plugin := reportingplugins.NewMedianHeightReportingPlugin(config, requestsStore)

	// Execute
	query, err := plugin.Query(ctx, ocr3types.OutcomeContext{})

	// Verify
	require.NoError(t, err)
	assert.NotNil(t, query)

	// Unmarshal query
	var requestIDs []string
	err = json.Unmarshal(query, &requestIDs)
	require.NoError(t, err)
	assert.ElementsMatch(t, requestIDs, []string{reqID1, reqID2})
}

func TestMedianHeightReportingPlugin_Observation(t *testing.T) {
	// Setup
	config := ocr3types.ReportingPluginConfig{}
	requestsStore, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	reqID1 := "reqID1"
	_, err = requestsStore.StartConsensusRequest(ctx, reqID1, 10)
	require.NoError(t, err)
	reqID2 := "reqID2"
	_, err = requestsStore.StartConsensusRequest(ctx, reqID2, 10)
	require.NoError(t, err)

	err = requestsStore.AddObservationForRequest(ctx, reqID1, 100, []byte("value1"))
	require.NoError(t, err)
	err = requestsStore.AddObservationForRequest(ctx, reqID1, 101, []byte("value11"))
	require.NoError(t, err)
	err = requestsStore.AddObservationForRequest(ctx, reqID2, 200, []byte("value2"))
	require.NoError(t, err)

	plugin := reportingplugins.NewMedianHeightReportingPlugin(config, requestsStore)

	// Mock query
	requestIDs := []string{reqID1, reqID2}
	query, err := json.Marshal(requestIDs)
	require.NoError(t, err)

	// Execute
	observation, err := plugin.Observation(ctx, ocr3types.OutcomeContext{}, query)

	// Verify
	require.NoError(t, err)
	assert.NotNil(t, observation)

	// Unmarshal observation
	var requestsLatestHeight []reportingplugins.RequestObservationHeight
	err = json.Unmarshal(observation, &requestsLatestHeight)
	require.NoError(t, err)

	// Ensure the highest height is returned
	expected := []reportingplugins.RequestObservationHeight{
		{RequestID: reqID1, Height: 101},
		{RequestID: reqID2, Height: 200},
	}
	assert.ElementsMatch(t, requestsLatestHeight, expected)
}

func TestMedianHeightReportingPlugin_ValidateObservation_RejectsDuplicateObservations(t *testing.T) {
	// Setup
	config := ocr3types.ReportingPluginConfig{}
	requestsStore, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	reqID1 := "reqID1"
	_, err = requestsStore.StartConsensusRequest(ctx, reqID1, 10)
	require.NoError(t, err)

	plugin := reportingplugins.NewMedianHeightReportingPlugin(config, requestsStore)

	// Mock observation with duplicate requestID
	requestsLatestHeight := []reportingplugins.RequestObservationHeight{
		{RequestID: reqID1, Height: 100},
		{RequestID: reqID1, Height: 200},
	}
	observation, err := json.Marshal(requestsLatestHeight)
	require.NoError(t, err)

	// Execute
	err = plugin.ValidateObservation(ctx, ocr3types.OutcomeContext{}, nil, types.AttributedObservation{Observation: observation})

	// Verify
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate request ID found")
}

func TestMedianHeightReportingPlugin_Outcome(t *testing.T) {
	// Setup
	config := ocr3types.ReportingPluginConfig{}
	requestsStore, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	reqID1 := "reqID1"
	_, err = requestsStore.StartConsensusRequest(ctx, reqID1, 10)
	require.NoError(t, err)
	reqID2 := "reqID2"
	_, err = requestsStore.StartConsensusRequest(ctx, reqID2, 10)
	require.NoError(t, err)

	plugin := reportingplugins.NewMedianHeightReportingPlugin(config, requestsStore)

	// Mock observations
	attributedObservations := []types.AttributedObservation{
		{Observation: mustMarshal(t, []reportingplugins.RequestObservationHeight{{RequestID: reqID1, Height: 100}})},
		{Observation: mustMarshal(t, []reportingplugins.RequestObservationHeight{{RequestID: reqID1, Height: 200}})},
		{Observation: mustMarshal(t, []reportingplugins.RequestObservationHeight{{RequestID: reqID1, Height: 300}})},
		{Observation: mustMarshal(t, []reportingplugins.RequestObservationHeight{{RequestID: reqID2, Height: 400}})},
		{Observation: mustMarshal(t, []reportingplugins.RequestObservationHeight{{RequestID: reqID2, Height: 500}})},
		{Observation: mustMarshal(t, []reportingplugins.RequestObservationHeight{{RequestID: reqID2, Height: 600}})},
	}

	// Execute
	outcome, err := plugin.Outcome(ctx, ocr3types.OutcomeContext{}, nil, attributedObservations)

	// Verify
	require.NoError(t, err)
	assert.NotNil(t, outcome)

	// Unmarshal outcome
	var medians []reportingplugins.RequestObservationHeight
	err = json.Unmarshal(outcome, &medians)
	require.NoError(t, err)

	// Ensure the median height is selected
	expected := []reportingplugins.RequestObservationHeight{
		{RequestID: reqID1, Height: 200},
		{RequestID: reqID2, Height: 500},
	}
	assert.ElementsMatch(t, medians, expected)
}

func TestMedianHeightReportingPlugin_Reports(t *testing.T) {
	config := ocr3types.ReportingPluginConfig{}
	requestsStore, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	reqID1 := "reqID1"
	_, err = requestsStore.StartConsensusRequest(ctx, reqID1, 10)
	require.NoError(t, err)
	reqID2 := "reqID2"
	_, err = requestsStore.StartConsensusRequest(ctx, reqID2, 10)
	require.NoError(t, err)

	plugin := reportingplugins.NewMedianHeightReportingPlugin(config, requestsStore)

	// Mock outcome
	medians := []reportingplugins.RequestObservationHeight{
		{RequestID: reqID1, Height: 200},
		{RequestID: reqID2, Height: 500},
	}
	outcome, err := json.Marshal(medians)
	require.NoError(t, err)

	// Execute
	reports, err := plugin.Reports(ctx, 1, outcome)

	// Verify
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, outcome, []byte(reports[0].ReportWithInfo.Report))
}

func TestMedianHeightReportingPlugin_ShouldTransmitAcceptedReport(t *testing.T) {
	config := ocr3types.ReportingPluginConfig{}
	requestsStore, err := requests.NewConsensusHandler()
	require.NoError(t, err)

	ctx := context.Background()
	reqID1 := "reqID1"
	_, err = requestsStore.StartConsensusRequest(ctx, reqID1, 10)
	require.NoError(t, err)
	reqID2 := "reqID2"
	_, err = requestsStore.StartConsensusRequest(ctx, reqID2, 10)
	require.NoError(t, err)

	plugin := reportingplugins.NewMedianHeightReportingPlugin(config, requestsStore)

	// Mock report with multiple medians
	medians := []reportingplugins.RequestObservationHeight{
		{RequestID: reqID1, Height: 200},
		{RequestID: reqID2, Height: 500},
	}
	report, err := json.Marshal(medians)
	require.NoError(t, err)

	// Execute
	shouldTransmit, err := plugin.ShouldTransmitAcceptedReport(ctx, 1, ocr3types.ReportWithInfo[[]byte]{Report: report})

	// Verify
	require.NoError(t, err)
	assert.True(t, shouldTransmit)

	// Mock report with a single median
	medians = []reportingplugins.RequestObservationHeight{
		{RequestID: reqID1, Height: 200},
	}
	report, err = json.Marshal(medians)
	require.NoError(t, err)

	// Execute
	shouldTransmit, err = plugin.ShouldTransmitAcceptedReport(ctx, 1, ocr3types.ReportWithInfo[[]byte]{Report: report})

	// Verify
	require.NoError(t, err)
	assert.True(t, shouldTransmit)

	// Mock report with no medians
	medians = []reportingplugins.RequestObservationHeight{}
	report, err = json.Marshal(medians)
	require.NoError(t, err)

	// Execute
	shouldTransmit, err = plugin.ShouldTransmitAcceptedReport(ctx, 1, ocr3types.ReportWithInfo[[]byte]{Report: report})

	// Verify
	require.NoError(t, err)
	assert.False(t, shouldTransmit)
}

func mustMarshal(t *testing.T, v interface{}) []byte {
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
