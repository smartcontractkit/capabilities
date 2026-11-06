package transmitter

import (
	"context"
	"errors"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/report"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/capabilities/internal/consensus/oracle"
	"github.com/smartcontractkit/capabilities/internal/consensus/oracle/plugin"
	oracletypes "github.com/smartcontractkit/capabilities/internal/consensus/oracle/types"
)

var _ ocr3types.ContractTransmitter[plugin.ReportInfo] = (*ContractTransmitter)(nil)

type SendResponse func(ctx context.Context, response oracle.ConsensusResponse)

type ContractTransmitter struct {
	lggr         logger.Logger
	sendResponse SendResponse
}

func (c *ContractTransmitter) Transmit(ctx context.Context, configDigest types.ConfigDigest, seqNr uint64, rwi ocr3types.ReportWithInfo[plugin.ReportInfo], signatures []types.AttributedOnchainSignature) error {
	requestID := rwi.Info.Value.RequestID
	if requestID == "" {
		return errors.New("report info has no request ID")
	}

	if failure := rwi.Info.Value.Failure; failure != nil {
		c.lggr.Debugw("received consensus failure message report", "requestID", requestID,
			"failureMessage", failure.Message, "failureCode", failure.Code)

		var failureErr caperrors.Error
		switch failure.Code {
		case oracletypes.ConsensusFailureCode_RECEIVED_FPLUS1_ERRORS:
			// This is considered to be a user error as the caller of the consensus capability has sent too many errors and
			// so consensus cannot be reached.
			failureErr = caperrors.NewPublicUserError(errors.New(failure.Message), caperrors.ConsensusFailed)
		case oracletypes.ConsensusFailureCode_MORE_THAN_ONE_VALID_OUTCOME_FOR_IDENTICAL_CONSENSUS:
			// This is considered to be a user error as the caller of the consensus capability is attempting to achieve
			// identical consensus on a value which has multiple valid (>=f+1) observations sets.  For example this
			// could occur if the caller is attempting to achieve identical consensus on a value which is relatively volatile
			// resulting in f+1 nodes seeing value A, and f+1 nodes seeing value B.
			failureErr = caperrors.NewPublicUserError(errors.New(failure.Message), caperrors.ConsensusFailed)
		case oracletypes.ConsensusFailureCode_NO_VALUES_MET_FPLUS1_THRESHOLD_FOR_IDENTICAL_CONSENSUS:
			// This is considered to be a user error as the caller of the consensus capability is attempting to achieve
			// identical consensus on a value which has no valid observation set with >=f+1 observations. For example this
			// could occur if the caller is attempting to achieve identical consensus on a value which is relatively volatile
			// resulting in f+1 nodes seeing value A, and f+1 nodes seeing value B.
			failureErr = caperrors.NewPublicUserError(errors.New(failure.Message), caperrors.ConsensusFailed)
		case oracletypes.ConsensusFailureCode_NO_SINGLE_VALUE_TYPE_MET_FPLUS1_THRESHOLD_FOR_CONSENSUS:
			// This is considered to be a user error as the workflow is providing observations with different value types
			// (e.g. some nodes are reporting int256 values, and some nodes are reporting string values) and so consensus cannot be reached.
			failureErr = caperrors.NewPublicUserError(errors.New(failure.Message), caperrors.ConsensusFailed)
		default:
			failureErr = caperrors.NewPublicSystemError(errors.New(failure.Message), caperrors.ConsensusFailed)
		}

		response := oracle.ConsensusResponse{
			ReqID: requestID,
			SeqNr: seqNr,
			Err:   failureErr,
		}

		c.sendResponse(ctx, response)
	} else {
		c.lggr.Debugw("received consensus success report", "requestID", requestID)
		// report context is the config digest + the sequence number padded with zeros
		repContext := report.GenerateReportContext(seqNr, configDigest)

		response := oracle.ConsensusResponse{
			ReqID:         requestID,
			ConfigDigest:  configDigest,
			SeqNr:         seqNr,
			ReportContext: repContext,
			RawReport:     rwi.Report,
			Sigs:          signatures,
		}

		c.sendResponse(ctx, response)
	}

	return nil
}

func (c *ContractTransmitter) FromAccount(_ context.Context) (types.Account, error) {
	return "", nil
}

func NewContractTransmitter(lggr logger.Logger, sendResponse SendResponse) *ContractTransmitter {
	return &ContractTransmitter{lggr: lggr, sendResponse: sendResponse}
}
