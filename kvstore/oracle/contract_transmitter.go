package oracle

import (
	"context"
	"encoding/json"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ ocr3types.ContractTransmitter[[]byte] = (*contractTransmitter)(nil)

type contractTransmitter struct {
	identity      Identity
	logger        logger.Logger
	requestsStore *kvrequests.RequestsStore
}

func NewContractTransmitter(logger logger.Logger, identity Identity, requestsStore *kvrequests.RequestsStore) *contractTransmitter {
	return &contractTransmitter{
		logger:        logger,
		identity:      identity,
		requestsStore: requestsStore,
	}
}

// TODO: Implement the Transmit method - store the values in the report to the KV store
// Place success message in the outbox
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
	ct.logger.Debug("Updating request")

	return ct.requestsStore.Update(ctx, request)
}

// Unused: No external transmissions
func (ct *contractTransmitter) FromAccount() (types.Account, error) {
	return types.Account(ct.identity.EVMKey), nil
}
