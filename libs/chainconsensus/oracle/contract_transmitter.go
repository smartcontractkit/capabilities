package oracle

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

var _ ocr3types.ContractTransmitter[[]byte] = (*ContractTransmitter)(nil)

type ContractTransmitter struct {
	lggr          logger.SugaredLogger
	requestsStore RequestsHandler
}

func NewContractTransmitter(lggr logger.Logger, requestsStore RequestsHandler) *ContractTransmitter {
	return &ContractTransmitter{
		lggr:          logger.Sugared(lggr),
		requestsStore: requestsStore,
	}
}

func (ct *ContractTransmitter) Transmit(
	ctx context.Context,
	configDigest types.ConfigDigest,
	seqNr uint64,
	reportWithInfo ocr3types.ReportWithInfo[[]byte],
	attributedOnchainSignature []types.AttributedOnchainSignature,
) error {
	info, err := unmarshalReportInfo(reportWithInfo.Info)
	if err != nil {
		return fmt.Errorf("failed to unmarshal report info: %w", err)
	}

	reportType, err := getReportType(info)
	if err != nil {
		return fmt.Errorf("failed to get report type from report info: %w", err)
	}
	switch reportType {
	case reportTypeProtoReport:
		var report ctypes.RequestReport
		if err := proto.Unmarshal(reportWithInfo.Report, &report); err != nil {
			return fmt.Errorf("failed to unmarshal report: %w", err)
		}

		return ct.requestsStore.CompleteProtoRequest(report.RequestID, &report)
	case reportTypeHashable:
		id, err := getRequestID(info)
		if err != nil {
			return fmt.Errorf("failed to get request ID from report info: %w", err)
		}

		if len(reportWithInfo.Report) != ctypes.HashLength {
			return fmt.Errorf("invalid report length for hashable report: expected %d, got %d", ctypes.HashLength, len(reportWithInfo.Report))
		}
		var reportData ctypes.Hash
		copy(reportData[:], reportWithInfo.Report)
		return ct.requestsStore.CompleteHashableRequest(id, ctypes.NewHashableRequestReport(configDigest, seqNr, reportData, attributedOnchainSignature))
	default:
		return fmt.Errorf("unknown report type: %s", reportType)
	}
}

func getReportType(infoMap map[string]any) (string, error) {
	reportType, ok := infoMap[reportInfoKeyReportType]
	if !ok {
		return reportTypeProtoReport, nil
	}

	reportTypeStr, ok := reportType.(string)
	if !ok {
		return "", fmt.Errorf("report type is not a string")
	}

	return reportTypeStr, nil
}

func getRequestID(infoMap map[string]any) (string, error) {
	requestID, ok := infoMap[reportInfoKeyRequestID]
	if !ok {
		return "", fmt.Errorf("requestID not found in report info")
	}

	requestIDStr, ok := requestID.(string)
	if !ok {
		return "", fmt.Errorf("requestID is not a string")
	}

	return requestIDStr, nil
}

func unmarshalReportInfo(infoBytes []byte) (map[string]any, error) {
	infos := &structpb.Struct{}
	if err := proto.Unmarshal(infoBytes, infos); err != nil {
		return nil, fmt.Errorf("failed to unmarshal report info: %w", err)
	}

	return infos.AsMap(), nil
}

// This is unused and overwritten by the OracleFactory
func (ct *ContractTransmitter) FromAccount(_ context.Context) (types.Account, error) {
	return "", nil
}
