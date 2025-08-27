package oracle_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
)

func Test_Report_MedianTimeStamp(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md1.KeyBundleID = "evm"

	now := time.Now()
	timestamp1 := now.Add(1 * time.Second)
	timestamp2 := now.Add(2 * time.Second)
	timestamp3 := now.Add(3 * time.Second)

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newRRts([]byte("somerandombytes"), md1, timestamp2), newRRts([]byte("somerandombytes"), md1, timestamp3), newRRts([]byte("somerandombytes"), md1, timestamp1),
			newRRts([]byte("somerandombytes"), md1, timestamp3), newRRts([]byte("somerandombytes"), md1, timestamp1), newRRts([]byte("somerandombytes"), md1, timestamp2),
			newRRts([]byte("somerandombytes"), md1, timestamp2)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				meta, _, err := ocrtypes.Decode(report.ReportWithInfo.Report)
				require.NoError(t, err, "Failed to extract metadata fields from report")
				assert.Equal(t, uint32(timestamp2.Unix()), meta.Timestamp) // nolint
			}},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

// Test_MedianTimeStampsWithMismatchedObservationsIncludesAllTimestampsInCalculation checks that the median timestamp
// calculation includes all timestamps from the observations, even if those of observations that do not match the outcome.
// This matches the OCR1 behavior where the median is calculated from all timestamps, not just those that match the outcome.
func Test_Report_MedianTimeStampWithMismatchedObservationsIncludesAllTimestampsInCalculation(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md1.KeyBundleID = "evm"

	now := time.Now()
	timestamp1 := now.Add(1 * time.Second)
	timestamp2 := now.Add(2 * time.Second)
	timestamp3 := now.Add(3 * time.Second)

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newRRts([]byte("somerandombytes"), md1, timestamp1), newRRts([]byte("somerandombytes"), md1, timestamp1), newRRts([]byte("somerandombytes"), md1, timestamp1),
			newRRts([]byte("somerandombytes2"), md1, timestamp2), newRRts([]byte("somerandombytes2"), md1, timestamp2), newRRts([]byte("somerandombytes"), md1, timestamp3),
			newRRts([]byte("somerandombytes"), md1, timestamp3)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				meta, _, err := ocrtypes.Decode(report.ReportWithInfo.Report)
				require.NoError(t, err, "Failed to extract metadata fields from report")
				assert.Equal(t, uint32(timestamp2.Unix()), meta.Timestamp) // nolint
			}},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func Test_ReceivedIdenticalReportFromAllNodes(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1),
			newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1),
			newRR([]byte("somerandombytes"), md1)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				require.True(t, bytes.HasSuffix(report.ReportWithInfo.Report, []byte("somerandombytes")), "Report does not end with 'somerandombytes'")
				require.Equal(t, md1.RequestID(), infos.AsMap()[oracle.InfoRequestID], "RequestID does not match md1.RequestID")
				require.Equal(t, md1.KeyBundleID, infos.AsMap()["keyBundleName"], "KeyBundleID does not match md1.KeyBundleID")

				meta, _, err := ocrtypes.Decode(report.ReportWithInfo.Report)
				require.NoError(t, err, "Failed to extract metadata fields from report")

				require.Equal(t, md1.WorkflowExecutionID, meta.ExecutionID, "Metadata ExecutionID does not match")
				require.Equal(t, md1.WorkflowDonID, meta.DONID, "Metadata DONID does not match")
				require.Equal(t, md1.WorkflowDonConfigVersion, meta.DONConfigVersion, "Metadata DONConfigVersion does not match")
				require.Equal(t, md1.WorkflowID, meta.WorkflowID, "Metadata WorkflowID does not match")
				require.Equal(t, md1.WorkflowName, meta.WorkflowName, "Metadata WorkflowName does not match")
				require.Equal(t, md1.WorkflowOwner, meta.WorkflowOwner, "Metadata WorkflowOwner does not match")
				require.Equal(t, md1.ReportID, meta.ReportID, "Metadata WorkflowOwner does not match")
			}},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func Test_ReceivedIdenticalReportFromSufficientNodes(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newRR([]byte("somerandombytes2"), md1), newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1),
			newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1),
			newRR([]byte("somerandombytes2"), md1)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				require.True(t, bytes.HasSuffix(report.ReportWithInfo.Report, []byte("somerandombytes")), "Report does not end with 'somerandombytes'")
				require.Equal(t, md1.RequestID(), infos.AsMap()[oracle.InfoRequestID], "RequestID does not match md1.RequestID")
				require.Equal(t, md1.KeyBundleID, infos.AsMap()["keyBundleName"], "KeyBundleID does not match md1.KeyBundleID")

				meta, _, err := ocrtypes.Decode(report.ReportWithInfo.Report)
				require.NoError(t, err, "Failed to extract metadata fields from report")

				require.Equal(t, md1.WorkflowExecutionID, meta.ExecutionID, "Metadata ExecutionID does not match")
				require.Equal(t, md1.WorkflowDonID, meta.DONID, "Metadata DONID does not match")
				require.Equal(t, md1.WorkflowDonConfigVersion, meta.DONConfigVersion, "Metadata DONConfigVersion does not match")
				require.Equal(t, md1.WorkflowID, meta.WorkflowID, "Metadata WorkflowID does not match")
				require.Equal(t, md1.WorkflowName, meta.WorkflowName, "Metadata WorkflowName does not match")
				require.Equal(t, md1.WorkflowOwner, meta.WorkflowOwner, "Metadata WorkflowOwner does not match")
				require.Equal(t, md1.ReportID, meta.ReportID, "Metadata WorkflowOwner does not match")
			}},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func Test_SufficientAndInsufficentReportsInSingleRound(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md1.KeyBundleID = "evm"

	md2 := newRequestMetaData()
	md2.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1),
			newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1),
			newRR([]byte("somerandombytes"), md1)},
			verifyReport: func(t *testing.T, report ocr3types.ReportPlus[[]byte], infos *structpb.Struct) {
				require.True(t, bytes.HasSuffix(report.ReportWithInfo.Report, []byte("somerandombytes")), "Report does not end with 'somerandombytes'")
				require.Equal(t, md1.RequestID(), infos.AsMap()[oracle.InfoRequestID], "RequestID does not match md1.RequestID")
			}},

		md2.RequestID(): {requests: []*oracle.ConsensusRequest{
			newRR([]byte("somerandombytes2"), md2), newRR([]byte("somerandombytes4"), md2), newRR([]byte("somerandombytes3"), md2),
			newRR([]byte("somerandombytes"), md2), newRR([]byte("somerandombytes"), md2), newRR([]byte("somerandombytes3"), md2),
			newRR([]byte("somerandombytes2"), md2)},
			verifyReport: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func Test_ReceivedIdenticalReportFromInSufficientNodes(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	md1 := newRequestMetaData()
	md1.KeyBundleID = "evm"

	reqToObservations := map[string]consensusPluginTest{
		md1.RequestID(): {requests: []*oracle.ConsensusRequest{
			newRR([]byte("somerandombytes2"), md1), newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1),
			newRR([]byte("somerandombytes2"), md1), newRR([]byte("somerandombytes"), md1), newRR([]byte("somerandombytes"), md1),
			newRR([]byte("somerandombytes2"), md1)},
			verifyReport: nil},
	}

	runProtocolRoundTests(ctx, t, lggr, n, f, batchSize, reqToObservations)
}

func newRR(rawBytes []byte, metaData oracle.ConsensusRequestMetadata) *oracle.ConsensusRequest {
	return newRRts(rawBytes, metaData, time.Now())
}

func newRRts(rawBytes []byte, metaData oracle.ConsensusRequestMetadata, recievedAt time.Time) *oracle.ConsensusRequest {
	simpleConsensusInputs := &sdk.SimpleConsensusInputs{
		Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(values.NewBytes(rawBytes))},
		Descriptors: &sdk.ConsensusDescriptor{Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
	}

	return oracle.NewConsensusRequest(simpleConsensusInputs, recievedAt, time.Now().Add(1*time.Hour).UTC(), nil, metaData)
}
