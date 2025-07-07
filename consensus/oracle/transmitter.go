package oracle

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	pb2 "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"
)

var _ ocr3types.ContractTransmitter[[]byte] = (*ContractTransmitter)(nil)

type SendResponse func(ctx context.Context, requestID string, Value *pb2.Value)

type ContractTransmitter struct {
	lggr         logger.Logger
	sendResponse SendResponse
}

func (c *ContractTransmitter) Transmit(ctx context.Context, configDigest types.ConfigDigest, seqNr uint64, rwi ocr3types.ReportWithInfo[[]byte], signatures []types.AttributedOnchainSignature) error {
	outcome := &oracletypes.RequestOutcome{}
	err := proto.Unmarshal(rwi.Report, outcome)
	if err != nil {
		return fmt.Errorf("failed to unmarshal report: %w", err)
	}

	requestID := outcome.Metadata.RequestId
	serialisedValue := outcome.Outcome
	value := &pb2.Value{}
	if err := proto.Unmarshal(serialisedValue, value); err != nil {
		return fmt.Errorf("failed to unmarshal value for request %s: %w", requestID, err)
	}

	c.sendResponse(ctx, requestID, value)

	return nil
}

func (c *ContractTransmitter) FromAccount(_ context.Context) (types.Account, error) {
	return "", nil
}

func NewContractTransmitter(lggr logger.Logger, sendResponse SendResponse) *ContractTransmitter {
	return &ContractTransmitter{lggr: lggr, sendResponse: sendResponse}
}
