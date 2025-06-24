package oracle

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"

	ctypes "github.com/smartcontractkit/chain_capabilities/evm/consensus/types"
)

var _ ocr3types.ContractTransmitter[[]byte] = (*ContractTransmitter)(nil)

type ContractTransmitter struct {
	lggr          logger.SugaredLogger
	requestsStore RequestsStore
}

func NewContractTransmitter(lggr logger.Logger, requestsStore RequestsStore) *ContractTransmitter {
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
	var report evmservice.RequestReport
	if err := proto.Unmarshal(reportWithInfo.Report, &report); err != nil {
		return fmt.Errorf("failed to unmarshal report: %w", err)
	}

	switch report.RequestType {
	case evmservice.RequestType_REQUEST_TYPE_AGGREGATABLE, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT:
		ct.requestsStore.CompleteRequest(report.RequestID, report.GetValue())
		return nil
	case evmservice.RequestType_REQUEST_TYPE_LOCKABLE_TO_BLOCK:
	default:
		return fmt.Errorf("unsupported request type: %s %s", report.RequestType, report.RequestID)
	}

	rawRequest, ok := ct.requestsStore.GetRequest(report.RequestID)
	if !ok {
		ct.lggr.Warnf("lockable to a block request %s not found", report.RequestID)
		return nil
	}

	request, ok := rawRequest.(ctypes.LockableToBlockRequest)
	if !ok {
		ct.lggr.Warnf("lockable to a block request %s is of a different type %T", report.RequestID, rawRequest)
		return nil
	}

	height := report.GetHeight()
	if height == nil {
		return fmt.Errorf("lockable to a block request %s has no height", report.RequestID)
	}

	newRequest := request.ToEventuallyConsistent(report.GetHeight())
	ct.requestsStore.Update(newRequest)
	return nil
}

// This is unused and overwritten by the OracleFactory
func (ct *ContractTransmitter) FromAccount(_ context.Context) (types.Account, error) {
	return "", nil
}
