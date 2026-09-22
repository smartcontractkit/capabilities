package plugin_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/capabilities/consensus/metrics"
	"github.com/smartcontractkit/capabilities/consensus/oracle"
	"github.com/smartcontractkit/capabilities/consensus/oracle/plugin"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
)

// newReportsTestPlugin creates a reporting plugin for direct Reports() tests with the given report limits
func newReportsTestPlugin(t *testing.T, lggr logger.Logger, maxReportLengthBytes uint32, maxReportCount uint32) ocr3types.ReportingPlugin[[]byte] {
	t.Helper()

	reqStore := requests.NewStore[*oracle.ConsensusRequest]()
	metricsInstance, err := metrics.NewMetrics()
	require.NoError(t, err)

	reportingPlugin, err := plugin.NewReportingPlugin(lggr, metricsInstance, f, n, reqStore, oracle.NewObservationQuorumTracker(), &ocrtypes.ReportingPluginConfig{
		MaxQueryLengthBytes:              1000000,
		MaxObservationLengthBytes:        1000000,
		MaxOutcomeLengthBytes:            1000000,
		MaxReportLengthBytes:             maxReportLengthBytes,
		MaxReportCount:                   maxReportCount,
		HistoricalOutcomeExpirySeqNrSpan: 5,
	}, "evm", 1000)
	require.NoError(t, err)

	return reportingPlugin
}

func newReportsTestMetaData(requestID string, requestType oracletypes.RequestType) *oracletypes.RequestMetaData {
	return &oracletypes.RequestMetaData{
		RequestId:                requestID,
		WorkflowExecutionId:      "0102030405060708091011121314151617181920212223242526272829303132",
		WorkflowId:               "0039525c34de895c8fa68006bd63f6ce4a45ef1bc66377e791c6a8ae803dc0e4",
		WorkflowOwner:            "1139525c34de895c8fa68006bd634387a9f1192a",
		WorkflowName:             "a1b2c3d4e5f6a1b2c3d4",
		WorkflowDonId:            1,
		WorkflowDonConfigVersion: 1,
		ReportId:                 "abcd",
		KeyBundleId:              "evm",
		RequestType:              requestType,
	}
}

func newSuccessOutcome(metadata *oracletypes.RequestMetaData, outcome []byte, timestamp *timestamppb.Timestamp) *oracletypes.ConsensusOutcome {
	return &oracletypes.ConsensusOutcome{
		Outcome: &oracletypes.ConsensusOutcome_Success{
			Success: &oracletypes.ConsensusSuccessOutcome{
				Metadata:  metadata,
				Outcome:   outcome,
				Timestamp: timestamp,
			},
		},
	}
}

// serialiseValue serialises a value the way the outcome phase serialises a successful outcome
func serialiseValue(t *testing.T, value values.Value) []byte {
	t.Helper()
	serialisedValue, err := proto.MarshalOptions{Deterministic: true}.Marshal(values.Proto(value))
	require.NoError(t, err)
	return serialisedValue
}

func serialiseOutcome(t *testing.T, outcomes ...*oracletypes.ConsensusOutcome) []byte {
	t.Helper()
	serialisedOutcome, err := proto.MarshalOptions{Deterministic: true}.Marshal(&oracletypes.Outcome{Outcomes: outcomes})
	require.NoError(t, err)
	return serialisedOutcome
}

func reportInfoMap(t *testing.T, report ocr3types.ReportPlus[[]byte]) map[string]any {
	t.Helper()
	infos := &structpb.Struct{}
	require.NoError(t, proto.Unmarshal(report.ReportWithInfo.Info, infos))
	return infos.AsMap()
}

// verifyInvalidOutcomeReport checks that the report is a failure report with the INVALID_OUTCOME code for the request
func verifyInvalidOutcomeReport(t *testing.T, report ocr3types.ReportPlus[[]byte], expectedRequestID string, expectedFailureMessagePart string) {
	t.Helper()

	require.Empty(t, report.ReportWithInfo.Report, "invalid outcome should have an empty report body")

	infoMap := reportInfoMap(t, report)
	require.Equal(t, expectedRequestID, infoMap[plugin.InfoRequestID])
	require.Equal(t, "evm", infoMap[plugin.InfoKeyBundleName])
	require.Equal(t, oracletypes.ConsensusFailureCode_INVALID_OUTCOME.String(), infoMap[plugin.InfoConsensusFailureCode])
	require.Contains(t, infoMap[plugin.InfoConsensusFailureMessage], expectedFailureMessagePart)
}

func Test_Reports_InvalidOutcome_ReturnsFailure(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	reportingPlugin := newReportsTestPlugin(t, lggr, 10000, 100)
	now := timestamppb.Now()

	testCases := []struct {
		name                       string
		outcome                    *oracletypes.ConsensusOutcome
		expectedFailureMessagePart string
	}{
		{
			// This is what Report() puts on the wire for a ReportRequest with an empty EncodedPayload
			name:                       "report generation with an empty payload",
			outcome:                    newSuccessOutcome(newReportsTestMetaData("req", oracletypes.RequestType_REPORT_GENERATION), serialiseValue(t, values.NewBytes([]byte{})), now),
			expectedFailureMessagePart: "empty report payload",
		},
		{
			name:                       "report generation with a value that is not bytes",
			outcome:                    newSuccessOutcome(newReportsTestMetaData("req", oracletypes.RequestType_REPORT_GENERATION), serialiseValue(t, values.NewInt64(7)), now),
			expectedFailureMessagePart: "empty report payload",
		},
		{
			name:                       "value consensus with an empty outcome",
			outcome:                    newSuccessOutcome(newReportsTestMetaData("req", oracletypes.RequestType_VALUE_CONSENSUS), nil, now),
			expectedFailureMessagePart: "empty report payload",
		},
		{
			name:                       "unknown request type",
			outcome:                    newSuccessOutcome(newReportsTestMetaData("req", oracletypes.RequestType(99)), serialiseValue(t, values.NewBytes([]byte("payload"))), now),
			expectedFailureMessagePart: "unsupported request type",
		},
		{
			name:                       "missing timestamp",
			outcome:                    newSuccessOutcome(newReportsTestMetaData("req", oracletypes.RequestType_VALUE_CONSENSUS), serialiseValue(t, values.NewBytes([]byte("payload"))), nil),
			expectedFailureMessagePart: "has no timestamp",
		},
		{
			name:                       "timestamp that does not fit in the report metadata",
			outcome:                    newSuccessOutcome(newReportsTestMetaData("req", oracletypes.RequestType_VALUE_CONSENSUS), serialiseValue(t, values.NewBytes([]byte("payload"))), timestamppb.New(time.Unix(1<<33, 0))),
			expectedFailureMessagePart: "does not fit in the report metadata",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reports, err := reportingPlugin.Reports(ctx, 1, serialiseOutcome(t, tc.outcome))
			require.NoError(t, err, "Reports should not return an error for an invalid outcome")
			require.Len(t, reports, 1)

			verifyInvalidOutcomeReport(t, reports[0], "req", tc.expectedFailureMessagePart)
		})
	}

	t.Run("metadata that cannot be encoded", func(t *testing.T) {
		metadata := newReportsTestMetaData("req", oracletypes.RequestType_VALUE_CONSENSUS)
		metadata.WorkflowExecutionId = "not-hex"

		reports, err := reportingPlugin.Reports(ctx, 1, serialiseOutcome(t, newSuccessOutcome(metadata, serialiseValue(t, values.NewBytes([]byte("payload"))), now)))
		require.NoError(t, err)
		require.Len(t, reports, 1)

		verifyInvalidOutcomeReport(t, reports[0], "req", "failed to encode metadata")
	})

	t.Run("missing metadata is skipped", func(t *testing.T) {
		reports, err := reportingPlugin.Reports(ctx, 1, serialiseOutcome(t, newSuccessOutcome(nil, serialiseValue(t, values.NewBytes([]byte("payload"))), now)))
		require.NoError(t, err)
		require.Empty(t, reports, "an outcome without metadata cannot be attributed to a request")
	})
}

func Test_Reports_InvalidOutcome_DoesNotFailRound(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	reportingPlugin := newReportsTestPlugin(t, lggr, 10000, 100)
	now := timestamppb.Now()

	// The first outcome cannot be reported, the second one is valid
	invalidMetadata := newReportsTestMetaData("req-invalid", oracletypes.RequestType_VALUE_CONSENSUS)
	invalidMetadata.WorkflowExecutionId = "not-hex"
	validMetadata := newReportsTestMetaData("req-valid", oracletypes.RequestType_VALUE_CONSENSUS)
	serialisedValue := serialiseValue(t, values.NewBytes([]byte("payload")))

	reports, err := reportingPlugin.Reports(ctx, 1, serialiseOutcome(t,
		newSuccessOutcome(invalidMetadata, serialisedValue, now),
		newSuccessOutcome(validMetadata, serialisedValue, now),
	))
	require.NoError(t, err, "Reports should not return an error when a single outcome is invalid")
	require.Len(t, reports, 2, "Should have a report for each outcome")

	verifyInvalidOutcomeReport(t, reports[0], "req-invalid", "failed to encode metadata")

	// Verify the valid outcome is reported as a success with the expected metadata and payload
	infoMap := reportInfoMap(t, reports[1])
	require.Equal(t, "req-valid", infoMap[plugin.InfoRequestID])
	require.Nil(t, infoMap[plugin.InfoConsensusFailureCode], "Should not have failure code for successful report")

	meta, payload, err := ocrtypes.Decode(reports[1].ReportWithInfo.Report)
	require.NoError(t, err, "Failed to extract metadata fields from report")
	require.Equal(t, uint32(now.AsTime().Unix()), meta.Timestamp) //nolint:gosec // G115
	require.Equal(t, serialisedValue, payload)
	require.Len(t, reports[1].ReportWithInfo.Report, plugin.ReportMetaDataPrependLength+len(serialisedValue))
}

func Test_ReportCountLimit_IncludesOversizedReports(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	// Create a reporting plugin with a small max report length and count
	maxReportCount := uint32(3)
	reportingPlugin := newReportsTestPlugin(t, lggr, 150, maxReportCount)
	now := timestamppb.Now()

	// Create outcomes for more requests than the max report count, one of which produces an oversized report
	var outcomes []*oracletypes.ConsensusOutcome
	for i := 0; i < int(maxReportCount)+3; i++ {
		data := "small"
		if i == 1 {
			data = strings.Repeat("x", 200) // More than 150
		}
		outcomes = append(outcomes, newSuccessOutcome(newReportsTestMetaData("req-"+strconv.Itoa(i), oracletypes.RequestType_REPORT_GENERATION),
			serialiseValue(t, values.NewBytes([]byte(data))), now))
	}

	// Call Reports and verify the oversized report counts towards the limit like any other report
	reports, err := reportingPlugin.Reports(ctx, 1, serialiseOutcome(t, outcomes...))
	require.NoError(t, err)
	require.Len(t, reports, int(maxReportCount))
	require.Equal(t, oracletypes.ConsensusFailureCode_REPORT_TOO_LARGE.String(), reportInfoMap(t, reports[1])[plugin.InfoConsensusFailureCode])
}
