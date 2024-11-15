package oracle

import (
	"context"
	"encoding/json"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
)

var _ ocr3types.ContractTransmitter[[]byte] = (*contractTransmitter)(nil)

type contractTransmitter struct {
	logger        logger.Logger
	requestsStore *kvrequests.RequestsStore
}

func NewContractTransmitter(logger logger.Logger, requestsStore *kvrequests.RequestsStore) *contractTransmitter {
	return &contractTransmitter{
		logger:        logger,
		requestsStore: requestsStore,
	}
}

func (ct *contractTransmitter) Transmit(
	ctx context.Context,
	configDigest types.ConfigDigest,
	seqNr uint64,
	reportWithInfo ocr3types.ReportWithInfo[[]byte],
	attributedOnchainSignature []types.AttributedOnchainSignature,
) error {
	var request kvrequests.Request
	if err := json.Unmarshal(reportWithInfo.Report, &request); err != nil {
		return err
	}
	ct.logger.Debugw("Updating", "request", request)

	// If the request is to add or remove a namespace user, we don't need to store it
	if request.Type == kvrequests.RequestTypeAddNamespaceReference ||
		request.Type == kvrequests.RequestTypeRemoveNamespaceReference {
		ct.requestsStore.Remove(ctx, request.ID())
		return nil
	}

	return ct.requestsStore.Update(ctx, &request)
}

// This is unused and overwritten by the OracleFactory
func (ct *contractTransmitter) FromAccount(_ context.Context) (types.Account, error) {
	return "", nil
}
