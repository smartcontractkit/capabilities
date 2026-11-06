package plugin

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	oracletypes "github.com/smartcontractkit/capabilities/internal/consensus/oracle/types"
	"github.com/smartcontractkit/capabilities/libs/ocr"

	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
)

const ReportMetaDataPrependLength = 109

// Info is what the transmitter needs of a report.
type Info struct {
	RequestID string
	// Failure is set when consensus on the request failed.
	Failure *Failure
}

type Failure struct {
	Code    oracletypes.ConsensusFailureCode
	Message string
}

// ReportInfo is the info of a consensus report. Its KeyBundle names the node's key bundle that signs the report.
type ReportInfo = ocr.ReportInfo[Info]

func (r *reportingPlugin) Reports(ctx context.Context, seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportPlus[ReportInfo], error) {
	requestsOutcome := &oracletypes.Outcome{}
	err := proto.Unmarshal(outcome, requestsOutcome)
	if err != nil {
		return nil, err
	}

	var reports []ocr3types.ReportPlus[ReportInfo]
	var successIDs, failureIDs []string

	// Create a report for each outcome
	for _, reqOutcome := range requestsOutcome.Outcomes {
		switch v := reqOutcome.GetOutcome().(type) {
		case *oracletypes.ConsensusOutcome_Success:
			successOutcome := v.Success
			r.lggr.Debugw("received successful consensus outcome", "seqNr", seqNr, "requestID", successOutcome.Metadata.RequestId)
			reqMetadata := successOutcome.Metadata
			var report []byte
			switch reqMetadata.RequestType {
			case oracletypes.RequestType_VALUE_CONSENSUS:
				report = successOutcome.Outcome
			case oracletypes.RequestType_REPORT_GENERATION:
				// If the request type is report extract the report from the values.Value before signing it
				serialisedValue := successOutcome.Outcome
				value := &valuespb.Value{}
				if err := proto.Unmarshal(serialisedValue, value); err != nil {
					return nil, fmt.Errorf("failed to unmarshal value for request %s: %w", reqMetadata.RequestId, err)
				}

				report = value.GetBytesValue()
				if report == nil {
					return nil, fmt.Errorf("failed to get report bytes for request %s", reqMetadata.RequestId)
				}
			}

			meta := ocrtypes.Metadata{
				Version:          1,
				ExecutionID:      reqMetadata.WorkflowExecutionId,
				Timestamp:        uint32(successOutcome.Timestamp.AsTime().Unix()), // nolint
				DONID:            reqMetadata.WorkflowDonId,
				DONConfigVersion: reqMetadata.WorkflowDonConfigVersion,
				WorkflowID:       reqMetadata.WorkflowId,
				WorkflowName:     reqMetadata.WorkflowName,
				WorkflowOwner:    reqMetadata.WorkflowOwner,
				ReportID:         reqMetadata.ReportId,
			}

			metadataPrepend, err := meta.Encode()
			if err != nil {
				return nil, fmt.Errorf("failed to encode metadata for request %s: %w", reqMetadata.RequestId, err)
			}

			reportWithMetaData := append(metadataPrepend, report...)

			// Check if the report is too large to transmit
			if len(reportWithMetaData) > r.maxReportLengthBytes {
				r.lggr.Errorw("report is too large to transmit", "seqNr", seqNr, "requestID", reqMetadata.RequestId,
					"reportSize", len(reportWithMetaData), "maxReportLengthBytes", r.maxReportLengthBytes)
				failureMsg := fmt.Sprintf(
					"report too large: the report for this request is %d bytes which exceeds the maximum allowed size of %d bytes; reduce the size of the data being returned",
					len(reportWithMetaData), r.maxReportLengthBytes)
				info := failedReportInfo(reqMetadata.RequestId, reqMetadata.KeyBundleId, failureMsg,
					oracletypes.ConsensusFailureCode_REPORT_TOO_LARGE)
				reports = append(reports, ocr3types.ReportPlus[ReportInfo]{
					ReportWithInfo: ocr3types.ReportWithInfo[ReportInfo]{
						Report: []byte{},
						Info:   info,
					},
					TransmissionScheduleOverride: nil,
				})
				continue
			}

			info := ReportInfo{KeyBundle: reqMetadata.KeyBundleId, Value: Info{RequestID: reqMetadata.RequestId}}
			reports = append(reports, ocr3types.ReportPlus[ReportInfo]{
				ReportWithInfo: ocr3types.ReportWithInfo[ReportInfo]{
					Report: reportWithMetaData,
					Info:   info,
				},
				TransmissionScheduleOverride: nil,
			})
			successIDs = append(successIDs, reqMetadata.RequestId)
		case *oracletypes.ConsensusOutcome_Failure:
			failedOutcome := v.Failure
			r.lggr.Debugw("received failed consensus outcome", "seqNr", seqNr, "requestID", failedOutcome.RequestID)
			info := failedReportInfo(failedOutcome.RequestID, failedOutcome.KeyBundleId, failedOutcome.FailureMessage,
				failedOutcome.Code)
			reports = append(reports, ocr3types.ReportPlus[ReportInfo]{
				ReportWithInfo: ocr3types.ReportWithInfo[ReportInfo]{
					Report: []byte{},
					Info:   info,
				},
				TransmissionScheduleOverride: nil,
			})
			failureIDs = append(failureIDs, failedOutcome.RequestID)
		default:
			r.lggr.Warnw("received unknown consensus outcome type", "seqNr", seqNr, "outcome", outcome)
		}

		if len(reports) == r.maxNumberOfReports {
			r.lggr.Warnw("maximum number of reports reached, stopping further report generation for this round", "seqNr", seqNr, "maxNumberOfReports", r.maxNumberOfReports)
			break
		}
	}

	r.lggr.Debugw("consensus plugin reports complete", "seqNr", seqNr, "numReports", len(reports), "successIDs", successIDs, "failureIDs", failureIDs)
	return reports, nil
}

func failedReportInfo(requestID string, keyBundleID string, failureMessage string,
	failureCode oracletypes.ConsensusFailureCode) ReportInfo {
	return ReportInfo{
		KeyBundle: keyBundleID,
		Value:     Info{RequestID: requestID, Failure: &Failure{Code: failureCode, Message: failureMessage}},
	}
}
