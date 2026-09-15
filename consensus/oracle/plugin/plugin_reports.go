package plugin

import (
	"context"
	"fmt"
	"math"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
)

const ReportMetaDataPrependLength = 109

const InfoRequestID = "requestID"
const InfoConsensusFailureMessage = "failureMessage"
const InfoConsensusFailureCode = "failureCode"
const InfoKeyBundleName = "keyBundleName"

func (r *reportingPlugin) Reports(ctx context.Context, seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	requestsOutcome := &oracletypes.Outcome{}
	err := proto.Unmarshal(outcome, requestsOutcome)
	if err != nil {
		return nil, err
	}

	var reports []ocr3types.ReportPlus[[]byte]
	var successIDs, failureIDs []string

	// Create a report for each outcome
	for _, reqOutcome := range requestsOutcome.Outcomes {
		// The limit is checked before the next outcome is processed so that every report added to the round, including
		// failure reports, counts towards it
		if r.maxNumberOfReports > 0 && len(reports) >= r.maxNumberOfReports {
			r.lggr.Warnw("maximum number of reports reached, stopping further report generation for this round", "seqNr", seqNr, "maxNumberOfReports", r.maxNumberOfReports)
			break
		}

		switch v := reqOutcome.GetOutcome().(type) {
		case *oracletypes.ConsensusOutcome_Success:
			successOutcome := v.Success
			reqMetadata := successOutcome.Metadata
			if reqMetadata == nil {
				// Without the metadata there is no request ID or key bundle to attribute a failure report to
				r.lggr.Errorw("received successful consensus outcome without metadata, skipping", "seqNr", seqNr)
				continue
			}
			r.lggr.Debugw("received successful consensus outcome", "seqNr", seqNr, "requestID", reqMetadata.RequestId)

			reportWithMetaData, failure := r.buildSuccessReport(successOutcome)
			if failure != nil {
				// The request is failed on its own so that the remaining outcomes in the round are still reported
				r.lggr.Errorw("unable to build report for successful consensus outcome", "seqNr", seqNr, "requestID", reqMetadata.RequestId,
					"failureCode", failure.code.String(), "failureMessage", failure.message)
				info, err := createFailedConsensusReportInfo(reqMetadata.RequestId, reqMetadata.KeyBundleId, failure.message, failure.code)
				if err != nil {
					return nil, fmt.Errorf("failed to create report info for invalid consensus outcome %s: %w", reqMetadata.RequestId, err)
				}

				reports = append(reports, newFailureReport(info))
				failureIDs = append(failureIDs, reqMetadata.RequestId)
				continue
			}

			info, err := createSuccessfulConsensusReportInfo(reqMetadata)
			if err != nil {
				return nil, fmt.Errorf("failed to create report info for successful consensus request %s: %w", reqMetadata.RequestId, err)
			}

			reports = append(reports, ocr3types.ReportPlus[[]byte]{
				ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
					Report: reportWithMetaData,
					Info:   info,
				},
				TransmissionScheduleOverride: nil,
			})
			successIDs = append(successIDs, reqMetadata.RequestId)
		case *oracletypes.ConsensusOutcome_Failure:
			failedOutcome := v.Failure
			r.lggr.Debugw("received failed consensus outcome", "seqNr", seqNr, "requestID", failedOutcome.RequestID)
			info, err := createFailedConsensusReportInfo(failedOutcome.RequestID, failedOutcome.KeyBundleId, failedOutcome.FailureMessage,
				failedOutcome.Code)
			if err != nil {
				return nil, fmt.Errorf("failed to create report info for failed consensus outcome %s: %w", failedOutcome.RequestID, err)
			}

			reports = append(reports, newFailureReport(info))
			failureIDs = append(failureIDs, failedOutcome.RequestID)
		default:
			r.lggr.Warnw("received unknown consensus outcome type", "seqNr", seqNr, "outcome", outcome)
		}
	}

	r.lggr.Debugw("consensus plugin reports complete", "seqNr", seqNr, "numReports", len(reports), "successIDs", successIDs, "failureIDs", failureIDs)
	return reports, nil
}

// reportFailure describes why a successful consensus outcome could not be turned into a report to sign. It is returned
// to the caller as a failed request in place of the report.
type reportFailure struct {
	message string
	code    oracletypes.ConsensusFailureCode
}

func newInvalidOutcomeFailure(format string, args ...any) *reportFailure {
	return &reportFailure{
		message: fmt.Sprintf(format, args...),
		code:    oracletypes.ConsensusFailureCode_INVALID_OUTCOME,
	}
}

// buildSuccessReport builds the report to be signed for a successful consensus outcome, which is the encoded report
// metadata followed by the report payload. The outcome is validated first, as the DON attests to every byte of the
// report and a consumer cannot tell a report with an empty payload apart from a genuine result.
func (r *reportingPlugin) buildSuccessReport(successOutcome *oracletypes.ConsensusSuccessOutcome) ([]byte, *reportFailure) {
	reqMetadata := successOutcome.Metadata

	var report []byte
	switch reqMetadata.RequestType {
	case oracletypes.RequestType_VALUE_CONSENSUS:
		report = successOutcome.Outcome
	case oracletypes.RequestType_REPORT_GENERATION:
		// If the request type is report extract the report from the values.Value before signing it
		value := &valuespb.Value{}
		if err := proto.Unmarshal(successOutcome.Outcome, value); err != nil {
			return nil, newInvalidOutcomeFailure("failed to unmarshal value for request %s: %v", reqMetadata.RequestId, err)
		}

		report = value.GetBytesValue()
	default:
		return nil, newInvalidOutcomeFailure("unsupported request type %s for request %s", reqMetadata.RequestType, reqMetadata.RequestId)
	}

	// A nil check alone would not catch a present but empty payload, such as a report request with an empty encoded payload
	if len(report) == 0 {
		return nil, newInvalidOutcomeFailure("consensus outcome for request %s has an empty report payload", reqMetadata.RequestId)
	}

	if successOutcome.Timestamp == nil {
		return nil, newInvalidOutcomeFailure("consensus outcome for request %s has no timestamp", reqMetadata.RequestId)
	}

	// The report metadata carries the timestamp as a uint32 unix time, so a value outside of that range would otherwise
	// be silently truncated. A zero timestamp is deliberately not rejected here: until every node sets
	// include_error_observation_timestamps_flag the default value path can legitimately produce one, and all nodes must
	// build identical reports for the same outcome.
	unixTimestamp := successOutcome.Timestamp.AsTime().Unix()
	if unixTimestamp < 0 || unixTimestamp > math.MaxUint32 {
		return nil, newInvalidOutcomeFailure("consensus outcome for request %s has timestamp %d which does not fit in the report metadata", reqMetadata.RequestId, unixTimestamp)
	}

	meta := ocrtypes.Metadata{
		Version:          1,
		ExecutionID:      reqMetadata.WorkflowExecutionId,
		Timestamp:        uint32(unixTimestamp), //nolint:gosec // G115 - range checked above
		DONID:            reqMetadata.WorkflowDonId,
		DONConfigVersion: reqMetadata.WorkflowDonConfigVersion,
		WorkflowID:       reqMetadata.WorkflowId,
		WorkflowName:     reqMetadata.WorkflowName,
		WorkflowOwner:    reqMetadata.WorkflowOwner,
		ReportID:         reqMetadata.ReportId,
	}

	metadataPrepend, err := meta.Encode()
	if err != nil {
		return nil, newInvalidOutcomeFailure("failed to encode metadata for request %s: %v", reqMetadata.RequestId, err)
	}

	reportWithMetaData := append(metadataPrepend, report...)

	// Check if the report is too large to transmit
	if len(reportWithMetaData) > r.maxReportLengthBytes {
		return nil, &reportFailure{
			message: fmt.Sprintf(
				"report too large: the report for this request is %d bytes which exceeds the maximum allowed size of %d bytes; reduce the size of the data being returned",
				len(reportWithMetaData), r.maxReportLengthBytes),
			code: oracletypes.ConsensusFailureCode_REPORT_TOO_LARGE,
		}
	}

	return reportWithMetaData, nil
}

// newFailureReport creates a report with an empty body, as only the info is used to return a failure to the caller
func newFailureReport(info []byte) ocr3types.ReportPlus[[]byte] {
	return ocr3types.ReportPlus[[]byte]{
		ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
			Report: []byte{},
			Info:   info,
		},
		TransmissionScheduleOverride: nil,
	}
}

// The report info is created as a map else the OCR3OnchainKeyringMultiChainAdapter will not work.
// OCR3OnchainKeyringMultiChainAdapter (in core) requires that the key bundle id is added to the map with the key
// "keyBundleName".
func createSuccessfulConsensusReportInfo(reqMetadata *oracletypes.RequestMetaData) ([]byte, error) {
	infos, err := structpb.NewStruct(map[string]any{
		InfoKeyBundleName: reqMetadata.KeyBundleId,
		InfoRequestID:     reqMetadata.RequestId,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create structpb for report info: %w", err)
	}

	infoBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(infos)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal report info: %w", err)
	}

	return infoBytes, nil
}

func createFailedConsensusReportInfo(requestID string, keyBundleID string, failureMessage string,
	failureCode oracletypes.ConsensusFailureCode) ([]byte, error) {
	infos, err := structpb.NewStruct(map[string]any{
		InfoKeyBundleName:           keyBundleID,
		InfoRequestID:               requestID,
		InfoConsensusFailureMessage: failureMessage,
		InfoConsensusFailureCode:    failureCode.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create structpb for report info: %w", err)
	}

	infoBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(infos)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal report info: %w", err)
	}

	return infoBytes, nil
}
