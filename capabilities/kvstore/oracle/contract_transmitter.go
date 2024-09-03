package oracle

import (
	"context"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

var _ ocr3types.ContractTransmitter[[]byte] = (*contractTransmitter)(nil)

type contractTransmitter struct{}

func NewContractTransmitter() *contractTransmitter {
	return &contractTransmitter{}
}

// TODO: Implement the Transmit method
func (ct *contractTransmitter) Transmit(
	context.Context,
	types.ConfigDigest,
	uint64,
	ocr3types.ReportWithInfo[[]byte],
	[]types.AttributedOnchainSignature,
) error {
	return nil
}

// TODO: Implement the FromAccount method
func (ct *contractTransmitter) FromAccount() (types.Account, error) {
	return "", nil
}
