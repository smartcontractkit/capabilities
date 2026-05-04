package actions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/gagliardetto/solana-go"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
)

type TransmissionState uint8

const (
	TransmissionStateNotAttempted TransmissionState = iota
	TransmissionStateSucceeded
	TransmissionStateFailed
)

type TransmissionInfo struct {
	State     TransmissionState
	Signature solana.Signature
}
type TransmissionInfoProvider interface {
	GetTransmissionInfo(ctx context.Context, transmissionID [32]byte) (TransmissionInfo, error)
}

type CREForwarderClient interface {
	InvokeOnReport(ctx context.Context, receiver solana.PublicKey, meta []*solcap.AccountMeta, report *sdk.ReportResponse, gasConfig *solcap.ComputeConfig) (*soltypes.SubmitTransactionReply, error)
}

type WriteReport struct {
	types.SolanaService
	forwarderClient          CREForwarderClient
	transmissionInfoProvider TransmissionInfoProvider
	ReceiverGasMinimum       uint64
	chainSelector            uint64

	lggr              logger.Logger
	beholderProcessor beholder.ProtoProcessor
	messageBuilder    *monitoring.MessageBuilder

	txComputeLimit        limits.BoundLimiter[uint32]
	reportSizeLimit       limits.BoundLimiter[commoncfg.Size]
	transmissionScheduler ts.TransmissionScheduler
}

func (s *Solana) WriteReport(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.WriteReportRequest) (*capabilities.ResponseAndMetadata[*solcap.WriteReportReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}
	monitoring.EmitInitiated(ctx, s.lggr, s.beholderProcessor, s.messageBuilder.BuildWriteReportInitiated(telemetryContext, input))
	// 1. Validate inputs
	err := s.validateInputsAndReportMetadata(metadata, input)
	if err != nil {
		monitoring.LogAndEmitError(ctx, s.lggr, s.beholderProcessor, s.messageBuilder.BuildWriteReportError(telemetryContext, input, "Failed to WriteReport, user error due to invalid request", err.Error(), true))
		return nil, NewUserError(err)
	}

	report, billingMetadata, err := s.executeWriteReport(ctx, input, metadata, telemetryContext)
	if err != nil {
		isUserError := s.isUserErrorWriteReport(err)
		monitoring.LogAndEmitError(ctx, s.lggr, s.beholderProcessor,
			s.messageBuilder.BuildWriteReportError(telemetryContext, input, "Failed to WriteReport while checking if the report exists or trying to publish on chain", err.Error(), isUserError))
		return nil, GetError(err, isUserError)
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully WriteReport execution", s.lggr, s.beholderProcessor, s.messageBuilder.BuildWriteReportSuccess(telemetryContext, input))
	responseAndMetadata := capabilities.ResponseAndMetadata[*solcap.WriteReportReply]{
		Response:         report,
		ResponseMetadata: billingMetadata,
	}

	return &responseAndMetadata, nil
}

const UnknownIssueExecutingReceiverContractMessage = "unknown issue execution receiver contract"

func (s *Solana) executeWriteReport(ctx context.Context, request *solcap.WriteReportRequest, metadata capabilities.RequestMetadata, telemetryContext monitoring.TelemetryContext) (*solcap.WriteReportReply, capabilities.ResponseMetadata, error) {
	wr := &WriteReport{
		SolanaService:            s.SolanaService,
		forwarderClient:          s.forwarderClient,
		transmissionInfoProvider: s.transmissionInfoProvider,
		chainSelector:            s.chainSelector,
		txComputeLimit:           s.txComputeLimit,
		reportSizeLimit:          s.reportSizeLimit,
		lggr:                     s.messageBuilder.RequestLggr(s.lggr, telemetryContext),
		beholderProcessor:        s.beholderProcessor,
		messageBuilder:           s.messageBuilder,
		transmissionScheduler:    s.transmissionScheduler,
	}

	return wr.executeWriteReport(ctx, request, telemetryContext, metadata)
}

func (wr *WriteReport) executeWriteReport(
	ctx context.Context,
	request *solcap.WriteReportRequest,
	telemetryContext monitoring.TelemetryContext,
	metadata capabilities.RequestMetadata,
) (*solcap.WriteReportReply, capabilities.ResponseMetadata, error) {
	ctx = contexts.WithChainSelector(ctx, wr.chainSelector)
	receiver := solana.PublicKey(request.Receiver)
	transmissionID, err := extractTransmissionID(receiver, request.GetReport())
	if err != nil {
		return nil, capabilities.ResponseMetadata{}, err
	}

	if request.ComputeConfig == nil {
		request.ComputeConfig = &solcap.ComputeConfig{}
		limit, limErr := wr.txComputeLimit.Limit(ctx)
		if limErr != nil {
			return nil, capabilities.ResponseMetadata{}, limErr
		}
		request.ComputeConfig.ComputeLimit = limit
	} else {
		err = wr.txComputeLimit.Check(ctx, request.ComputeConfig.ComputeLimit)
		if err != nil {
			return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s provided compute config exceeds limit (computeLimit=%d): %w", capcommon.UserError, request.ComputeConfig.ComputeLimit, err)
		}
	}

	transmissionIDStr := hex.EncodeToString(transmissionID[:])
	queuePosition := wr.transmissionScheduler.GetQueuePosition(transmissionIDStr)
	wr.lggr = logger.With(wr.lggr, "queuePosition", queuePosition)

	var transmissionInfo TransmissionInfo
	if queuePosition <= 0 {
		transmissionInfo, err = capcommon.WithQuickRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
			return wr.transmissionInfoProvider.GetTransmissionInfo(ctx, transmissionID)
		})
	} else {
		transmissionInfo, err = wr.pollTransmissionInfo(ctx, transmissionID, queuePosition)
	}

	if err != nil {
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed to get transmission info: %w", err)
	}

	switch transmissionInfo.State {
	case TransmissionStateNotAttempted:
		wr.lggr.Infow(
			"transmission not attempted - submitting",
			"receiver", receiver.String(),
		)

	case TransmissionStateSucceeded:
		wr.lggr.Infow(
			"returning without a transmission attempt - report already onchain",
			"signature", transmissionInfo.Signature.String(),
		)
		return wr.successWriteReportReply(&transmissionInfo.Signature), capabilities.ResponseMetadata{}, nil

	case TransmissionStateFailed:
		wr.lggr.Infow(
			"returning without a transmission attempt - transmission already attempted and failed",
			"signature", transmissionInfo.Signature.String(),
		)
		return wr.failedWriteReportReply(&transmissionInfo.Signature, capcommon.Ptr(UnknownIssueExecutingReceiverContractMessage)), capabilities.ResponseMetadata{}, nil

	default:
		return wr.fatalWriteReportReply(fmt.Sprintf("unexpected transmission state: %d", transmissionInfo.State)), capabilities.ResponseMetadata{}, nil
	}

	if err := wr.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport)); err != nil {
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s report size exceeds limit: %w", capcommon.UserError, err)
	}

	wr.lggr.Debugw("Submitting transaction for report", "executionID", metadata.WorkflowExecutionID)
	_, err = wr.forwarderClient.InvokeOnReport(
		ctx,
		receiver,
		request.GetRemainingAccounts(),
		request.GetReport(),
		request.GetComputeConfig(),
	)
	if err != nil {
		wr.lggr.Errorw("Transaction failed", "error", err.Error())
		return nil, capabilities.ResponseMetadata{}, err
	}

	var last TransmissionInfo
	last, err = capcommon.WithPollingRetry(ctx, wr.lggr, func(c context.Context) (TransmissionInfo, error) {
		ti, tiErr := wr.transmissionInfoProvider.GetTransmissionInfo(c, transmissionID)
		if tiErr != nil {
			return TransmissionInfo{}, tiErr
		}
		// If still NotAttempted, execution state account may not be committed yet.
		if ti.State == TransmissionStateNotAttempted {
			return TransmissionInfo{}, errors.New("tx submitted but transmission info not yet visible, retrying")
		}
		return ti, nil
	})

	if err != nil {
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed getting transmission info after submitting report, %w", err)
	}

	var meteringMetadata capabilities.ResponseMetadata
	transactionFee, err := wr.getFee(ctx, last.Signature)
	if err != nil {
		monitoring.LogAndEmitError(ctx, wr.lggr, wr.beholderProcessor, wr.messageBuilder.BuildWriteReportTxFeeCalculationError(telemetryContext, request, last.Signature, err.Error()))
	} else {
		meteringMetadata = metering.GetResponseMetadataWriteReport(transactionFee,
			wr.chainSelector)
	}

	switch last.State {
	case TransmissionStateSucceeded:
		wr.lggr.Infow("WriteReport succeeded", "executionID", metadata.WorkflowExecutionID, "signature", last.Signature.String())
		return wr.successWriteReportReply(&last.Signature), meteringMetadata, nil

	case TransmissionStateFailed:
		wr.lggr.Errorw("WriteReport failed (receiver execution reverted)", "executionID", metadata.WorkflowExecutionID, "signature", last.Signature.String())
		return wr.failedWriteReportReply(&last.Signature, capcommon.Ptr(UnknownIssueExecutingReceiverContractMessage)), meteringMetadata, nil

	default:
		return wr.fatalWriteReportReply(fmt.Sprintf("transmission state not expected after submit: %d", last.State)), meteringMetadata, nil
	}
}

func (s *Solana) isUserErrorWriteReport(err error) bool {
	return strings.HasPrefix(err.Error(), capcommon.UserError)
}

func (s *Solana) validateInputsAndReportMetadata(requestMetadata capabilities.RequestMetadata, request *solcap.WriteReportRequest) error {
	if request == nil {
		return errors.New("nil WriteReportRequest")
	}
	if request.Report == nil {
		return errors.New("nil SignedReport in WriteReportRequest")
	}
	if len(request.Receiver) != solana.PublicKeyLength {
		return fmt.Errorf("received public key is not 32 bytes long. key in hex: %s", hex.EncodeToString(request.Receiver))
	}
	if key := solana.PublicKey(request.Receiver); key.IsZero() {
		return fmt.Errorf("receiver public key is empty")
	}
	if err := validateRemainingAccountMetas(request.GetRemainingAccounts()); err != nil {
		return err
	}
	if len(request.Report.Sigs) == 0 {
		return fmt.Errorf("no signatures provided")
	}

	reportMetadata, err := capcommon.DecodeReportMetadata(request.Report.RawReport)
	if err != nil {
		return err
	}

	if reportMetadata.Version != 1 {
		return fmt.Errorf("unsupported report version: %d", reportMetadata.Version)
	}

	if reportMetadata.ExecutionID != requestMetadata.WorkflowExecutionID {
		return fmt.Errorf("workflowExecutionID in the report does not match WorkflowExecutionID in the request metadata. Report WorkflowExecutionID: %s, request WorkflowExecutionID: %s", reportMetadata.ExecutionID, requestMetadata.WorkflowExecutionID)
	}

	// case-insensitive verification of the owner address (so that a check-summed address matches its non-checksummed version).
	if !strings.EqualFold(reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner) {
		return fmt.Errorf("workflowOwner in the report does not match WorkflowOwner in the request metadata. Report WorkflowOwner: %s, request WorkflowOwner: %s", reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner)
	}

	//	workflowNames are padded to 10bytes
	decodedName := []byte(requestMetadata.WorkflowName)
	var workflowName [20]byte
	copy(workflowName[:], decodedName)
	if !bytes.Equal([]byte(reportMetadata.WorkflowName[:]), workflowName[:]) {
		return fmt.Errorf("workflowName in the report does not match WorkflowName in the request metadata. Report WorkflowName: %s, request WorkflowName: %s", reportMetadata.WorkflowName, hex.EncodeToString(workflowName[:]))
	}

	if reportMetadata.WorkflowID != requestMetadata.WorkflowID {
		return fmt.Errorf("workflowID in the report does not match WorkflowID in the request metadata. Report WorkflowID: %s, request WorkflowID: %s", reportMetadata.WorkflowID, requestMetadata.WorkflowID)
	}

	return nil
}

var (
	reportIDOffset    = 107
	reportIDSize      = 2
	executionIDOffset = 1
	executionIDSize   = 32
)

// transmissionID derivation logic is aligned with fowarder program
func extractTransmissionID(receiver solana.PublicKey, report *sdk.ReportResponse) ([32]byte, error) {
	var data []byte
	rawReport := report.RawReport
	if len(rawReport) < reportIDOffset+reportIDSize {
		return [32]byte{}, fmt.Errorf("invalid len of raw report: %d", len(rawReport))
	}

	// 1. add receiver
	data = append(data, receiver.Bytes()...)

	// 2. add executionID
	executionID := rawReport[executionIDOffset : executionIDOffset+executionIDSize]
	data = append(data, executionID...)

	// 3. add reportID
	reportID := rawReport[reportIDOffset : reportIDOffset+reportIDSize]
	data = append(data, reportID...)

	return sha256.Sum256(data), nil
}

// pollTransmissionInfo waits for the node's transmission slot then returns the current state.
// If another node transmits successfully or fails (F+1 times) before our slot, returns early.
func (wr *WriteReport) pollTransmissionInfo(
	ctx context.Context,
	transmissionID [32]byte,
	queuePosition int,
) (lastValid TransmissionInfo, err error) {
	delay := time.Duration(queuePosition) * wr.transmissionScheduler.DeltaStage
	wr.lggr.Infow("Polling until slot or state change", "delay", delay, "deltaStage", wr.transmissionScheduler.DeltaStage)

	attempt := 0
	stageTimer := time.NewTimer(delay)
	deltaStagePassed := false
	hadSuccessfulPoll := false
	defer func() {
		stageTimer.Stop()
		if !deltaStagePassed && hadSuccessfulPoll {
			wr.lggr.Infow("Transmission found before delta stage has passed")
		}
	}()

	for {
		if info, pollErr := wr.transmissionInfoProvider.GetTransmissionInfo(ctx, transmissionID); pollErr != nil {
			wr.lggr.Debugw("GetTransmissionInfo failed during polling", "error", pollErr, "attempt", attempt)
		} else {
			hadSuccessfulPoll = true
			lastValid = info
			switch lastValid.State {
			case TransmissionStateSucceeded, TransmissionStateFailed:
				return lastValid, nil
			case TransmissionStateNotAttempted:
			default:
				wr.lggr.Warnw("Unexpected transmission state during polling, continuing", "state", lastValid.State)
			}
		}

		wait := (100 * time.Millisecond) << min(attempt, 5)
		if wait > 2*time.Second {
			wait = 2 * time.Second
		}
		attempt++

		select {
		case <-ctx.Done():
			hadSuccessfulPoll = false
			return TransmissionInfo{}, fmt.Errorf("timed out waiting for transmission info")
		case <-stageTimer.C:
			deltaStagePassed = true
			if lastValid.State == TransmissionStateNotAttempted {
				if finalInfo, finalErr := wr.transmissionInfoProvider.GetTransmissionInfo(ctx, transmissionID); finalErr == nil {
					hadSuccessfulPoll = true
					lastValid = finalInfo
				} else {
					wr.lggr.Debugw("Final GetTransmissionInfo at stage boundary failed", "error", finalErr)
				}
			}
			if !hadSuccessfulPoll {
				wr.lggr.Errorw("All GetTransmissionInfo polls failed during delta stage window, cannot determine transmission state")
				return TransmissionInfo{}, fmt.Errorf("all GetTransmissionInfo polls failed during delta stage window")
			}
			wr.lggr.Infow("Delta stage has passed, returning transmission info")
			return lastValid, nil
		case <-time.After(wait):
		}
	}
}

func (wr *WriteReport) getFee(ctx context.Context, sig solana.Signature) (*big.Float, error) {
	tx, err := wr.GetTransaction(ctx, soltypes.GetTransactionRequest{Signature: soltypes.Signature(sig)})
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	feeInSol := new(big.Float).Quo(new(big.Float).SetUint64(tx.Meta.Fee), big.NewFloat(1e9))

	wr.lggr.Debugw("WriteReport fee", "feeInSol", feeInSol.String(), "feeInLamports", tx.Meta.Fee)
	return feeInSol, nil
}

func (wr *WriteReport) successWriteReportReply(sig *solana.Signature) *solcap.WriteReportReply {
	r := &solcap.WriteReportReply{}
	r.TxStatus = solcap.TxStatus_TX_STATUS_SUCCESS
	r.TxSignature = sig[:]

	return r
}

func (wr *WriteReport) failedWriteReportReply(sig *solana.Signature, msg *string) *solcap.WriteReportReply {
	r := &solcap.WriteReportReply{}
	r.TxSignature = sig[:]
	r.TxStatus = solcap.TxStatus_TX_STATUS_ABORTED
	r.ErrorMessage = msg

	return r
}

func (wr *WriteReport) fatalWriteReportReply(message string) *solcap.WriteReportReply {
	r := &solcap.WriteReportReply{}
	r.TxStatus = solcap.TxStatus_TX_STATUS_FATAL
	r.ErrorMessage = capcommon.Ptr(message)

	return r
}
