package plugin

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
)

const InfoRequestID = "requestID"
const ReportMetaDataPrependLength = 109

func (r *reportingPlugin) Reports(ctx context.Context, seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	requestsOutcome := &oracletypes.Outcome{}
	err := proto.Unmarshal(outcome, requestsOutcome)
	if err != nil {
		return nil, err
	}

	var reports []ocr3types.ReportPlus[[]byte]

	for _, requestOutcome := range requestsOutcome.Outcomes {
		// TODO as part of https://smartcontract-it.atlassian.net/browse/CAPPL-1076
		// handle other status outcomes
		if requestOutcome.Status != oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_SUCCESS {
			r.lggr.Debugw("skipping report generation for request as outcome status is not success", "requestID", requestOutcome.Metadata.RequestId, "status", requestOutcome.Status.String())
			continue
		}

		reqMetadata := requestOutcome.Metadata
		var report []byte
		switch reqMetadata.RequestType {
		case oracletypes.RequestType_VALUE_CONSENSUS:
			report = requestOutcome.Outcome
		case oracletypes.RequestType_REPORT_GENERATION:
			// If the request type is report extract the report from the values.Value before signing it
			serialisedValue := requestOutcome.Outcome
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
			Timestamp:        uint32(requestOutcome.Timestamp.AsTime().Unix()), // nolint
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

		info, err := createReportInfo(reqMetadata)
		if err != nil {
			return nil, fmt.Errorf("failed to create report info for request %s: %w", reqMetadata.RequestId, err)
		}

		reports = append(reports, ocr3types.ReportPlus[[]byte]{
			ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
				Report: reportWithMetaData,
				Info:   info,
			},
			TransmissionScheduleOverride: nil,
		})
	}

	r.lggr.Debug("consensus plugin reports complete, number of reports", len(reports))
	return reports, nil
}

// The report info is created as a map else the OCR3OnchainKeyringMultiChainAdapter will not work.
// OCR3OnchainKeyringMultiChainAdapter (in core) requires that the key bundle id is added to the map with the key
// "keyBundleName"
func createReportInfo(reqMetadata *oracletypes.RequestMetaData) ([]byte, error) {
	infos, err := structpb.NewStruct(map[string]any{
		"keyBundleName": reqMetadata.KeyBundleId,
		InfoRequestID:   reqMetadata.RequestId,
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
