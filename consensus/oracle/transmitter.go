package oracle

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/report"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
)

var _ ocr3types.ContractTransmitter[[]byte] = (*ContractTransmitter)(nil)

type SendResponse func(ctx context.Context, response ConsensusResponse)

type ContractTransmitter struct {
	lggr         logger.Logger
	sendResponse SendResponse
}

func (c *ContractTransmitter) Transmit(ctx context.Context, configDigest types.ConfigDigest, seqNr uint64,
	rwi ocr3types.ReportWithInfo[[]byte], signatures []types.AttributedOnchainSignature) error {
	unmarshalledInfo := new(structpb.Struct)
	err := proto.Unmarshal(rwi.Info, unmarshalledInfo)
	if err != nil {
		return fmt.Errorf("failed to unmarshal report info: %w", err)
	}
	infoMap := unmarshalledInfo.AsMap()
	requestID, ok := infoMap[InfoRequestID]
	if !ok {
		return errors.New("infoRequestID not found in report info")
	}

	requestIDStr, ok := requestID.(string)
	if !ok {
		return errors.New("infoRequestID is not a string")
	}

	// report context is the config digest + the sequence number padded with zeros
	repContext := report.GenerateReportContext(seqNr, configDigest)

	response := ConsensusResponse{
		ReqID:         requestIDStr,
		ConfigDigest:  configDigest,
		SeqNr:         seqNr,
		ReportContext: repContext,
		RawReport:     rwi.Report,
		Sigs:          signatures,
	}

	c.sendResponse(ctx, response)

	return nil
}

func (c *ContractTransmitter) FromAccount(_ context.Context) (types.Account, error) {
	return "", nil
}

func NewContractTransmitter(lggr logger.Logger, sendResponse SendResponse) *ContractTransmitter {
	return &ContractTransmitter{lggr: lggr, sendResponse: sendResponse}
}
