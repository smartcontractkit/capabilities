package oracle

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"google.golang.org/protobuf/proto"

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
	var report ctypes.RequestReport
	if err := proto.Unmarshal(reportWithInfo.Report, &report); err != nil {
		return fmt.Errorf("failed to unmarshal report: %w", err)
	}

	// TODO PLEX-1574: pass report signatures to workflow DON
	return ct.requestsStore.CompleteRequest(report.RequestID, &report)
}

// This is unused and overwritten by the OracleFactory
func (ct *ContractTransmitter) FromAccount(_ context.Context) (types.Account, error) {
	return "", nil
}
