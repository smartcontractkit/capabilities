package oracle

import (
	"context"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ ocr3types.ContractTransmitter[[]byte] = (*contractTransmitter)(nil)

type contractTransmitter struct {
	identity Identity
	logger   logger.Logger
}

func NewContractTransmitter(logger logger.Logger, identity Identity) *contractTransmitter {
	return &contractTransmitter{
		logger:   logger,
		identity: identity,
	}
}

// TODO: Implement the Transmit method - store the values in the report to the KV store
// Place success message in the outbox
func (ct *contractTransmitter) Transmit(
	context.Context,
	types.ConfigDigest,
	uint64,
	ocr3types.ReportWithInfo[[]byte],
	[]types.AttributedOnchainSignature,
) error {
	ct.logger.Debug("Transmitting report to KV store")
	return nil
}

// Unused: No external transmissions
func (ct *contractTransmitter) FromAccount() (types.Account, error) {
	return types.Account(ct.identity.EVMKey), nil
}
