package actions

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	types "github.com/smartcontractkit/chainlink-common/pkg/types"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

const (
	forwarderReportFunction              = "report"
	forwarderGetTransmissionInfoFunction = "get_transmission_info"
	defaultLedgerBoundsOffset            = uint32(20)
)

type TransmissionState uint32

const (
	TransmissionStateNotAttempted TransmissionState = iota
	TransmissionStateSucceeded
	TransmissionStateInvalidReceiver
	TransmissionStateFailed
)

type TransmissionInfo struct {
	State           TransmissionState
	Transmitter     string
	LedgerSequence  uint32
	Success         bool
	InvalidReceiver bool
}

type writeReport struct {
	service               types.StellarService
	lggr                  logger.SugaredLogger
	forwarderAddress      string
	chainSelector         uint64
	reportSizeLimit       limits.BoundLimiter[commoncfg.Size]
	transmissionScheduler ts.TransmissionScheduler
}

func (s *Stellar) WriteReport(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *stellarcap.WriteReportRequest,
) (*capabilities.ResponseAndMetadata[*stellarcap.WriteReportReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	if err := s.validateWriteReportInputs(metadata, input); err != nil {
		return nil, capcommon.NewUserError(err)
	}

	wr := &writeReport{
		service:               s.StellarService,
		lggr:                  s.lggr,
		forwarderAddress:      s.forwarderAddress,
		chainSelector:         s.chainSelector,
		reportSizeLimit:       s.reportSizeLimit,
		transmissionScheduler: s.transmissionScheduler,
	}

	reply, err := wr.execute(ctx, metadata, input)
	if err != nil {
		return nil, capcommon.GetError(err, s.isUserErrorWriteReport(err))
	}

	return &capabilities.ResponseAndMetadata[*stellarcap.WriteReportReply]{
		Response: reply,
	}, nil
}

func (wr *writeReport) execute(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	request *stellarcap.WriteReportRequest,
) (*stellarcap.WriteReportReply, error) {
	ctx = contexts.WithChainSelector(ctx, wr.chainSelector)

	transmissionID, workflowExecutionID, reportID, err := getTransmissionID(metadata.WorkflowExecutionID, request)
	if err != nil {
		return nil, err
	}

	queuePosition := wr.transmissionScheduler.GetQueuePosition(hex.EncodeToString(transmissionID[:]))
	wr.lggr = wr.lggr.With("queuePosition", queuePosition, "forwarder", wr.forwarderAddress, "receiver", request.ContractId)

	info, err := wr.pollTransmissionInfo(ctx, request.ContractId, workflowExecutionID, reportID, queuePosition)
	if err != nil {
		return nil, fmt.Errorf("failed to get transmission info: %w", err)
	}

	switch info.State {
	case TransmissionStateSucceeded:
		return wr.successReplyFromObservedState(info), nil
	case TransmissionStateInvalidReceiver:
		return wr.revertedReplyFromObservedState(info), nil
	case TransmissionStateFailed:
		return wr.revertedReplyFromObservedState(info), nil
	case TransmissionStateNotAttempted:
	default:
		return nil, fmt.Errorf("unexpected transmission state: %d", info.State)
	}

	if err := wr.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport)); err != nil {
		return nil, fmt.Errorf("%s report size exceeds limit: %w", capcommon.UserError, err)
	}

	args, err := buildForwarderReportArgs(request.ContractId, request.Report)
	if err != nil {
		return nil, err
	}

	simResp, err := wr.service.SimulateTransaction(ctx, stellartypes.SimulateTransactionRequest{
		ContractID:         wr.forwarderAddress,
		Function:           forwarderReportFunction,
		Args:               args,
		LedgerBoundsOffset: defaultLedgerBoundsOffset,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to simulate forwarder report call: %w", err)
	}
	if simResp.Error != "" {
		return nil, fmt.Errorf("%s forwarder simulation failed: %s", capcommon.UserError, simResp.Error)
	}

	txID := uuid.NewString()
	ops, err := buildInvokeContractOperationsXDR(wr.forwarderAddress, forwarderReportFunction, args)
	if err != nil {
		return nil, err
	}

	submitReply, err := wr.service.SubmitTransaction(ctx, stellartypes.SubmitTransactionRequest{
		ID:                 txID,
		OperationsXDR:      ops,
		LedgerBoundsOffset: defaultLedgerBoundsOffset,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to submit forwarder report transaction: %w", err)
	}

	txResult, txResultErr := wr.service.GetTransactionResult(ctx, txID)
	if txResultErr != nil {
		wr.lggr.Warnw("failed to fetch tx result after submit", "txID", txID, "error", txResultErr)
	}

	postInfo, err := capcommon.WithPollingRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
		readInfo, readErr := wr.getTransmissionInfo(ctx, request.ContractId, workflowExecutionID, reportID)
		if readErr != nil {
			return TransmissionInfo{}, readErr
		}
		if readInfo.State == TransmissionStateNotAttempted {
			return TransmissionInfo{}, errors.New("tx submitted but transmission info not yet visible on-chain, retrying")
		}
		return readInfo, nil
	})
	if err != nil {
		if txResultErr == nil {
			return wr.replyFromOwnTransaction(submitReply.TxHash, txResult), nil
		}
		return nil, fmt.Errorf("failed getting transmission info after node submitted the report on chain: %w", err)
	}

	switch postInfo.State {
	case TransmissionStateSucceeded:
		reply := wr.successReplyFromObservedState(postInfo)
		if txResultErr == nil {
			populateReplyFromTx(reply, submitReply.TxHash, txResult)
		}
		return reply, nil
	case TransmissionStateInvalidReceiver, TransmissionStateFailed:
		reply := wr.revertedReplyFromObservedState(postInfo)
		if txResultErr == nil {
			populateReplyFromTx(reply, submitReply.TxHash, txResult)
		}
		return reply, nil
	default:
		if txResultErr == nil {
			return wr.replyFromOwnTransaction(submitReply.TxHash, txResult), nil
		}
		return nil, fmt.Errorf("unexpected transmission state after submit: %d", postInfo.State)
	}
}

func (s *Stellar) validateWriteReportInputs(metadata capabilities.RequestMetadata, request *stellarcap.WriteReportRequest) error {
	if request == nil {
		return errors.New("nil WriteReportRequest")
	}
	if request.Report == nil {
		return errors.New("nil SignedReport in WriteReportRequest")
	}
	if request.ContractId == "" {
		return errors.New("contract_id is required")
	}
	if _, err := strkey.Decode(strkey.VersionByteContract, request.ContractId); err != nil {
		return fmt.Errorf("%s invalid receiver contract address: %w", capcommon.UserError, err)
	}
	if len(request.Report.Sigs) == 0 {
		return fmt.Errorf("%s signed report must contain at least one signature", capcommon.UserError)
	}

	reportMetadata, _, err := ocrtypes.Decode(request.Report.RawReport)
	if err != nil {
		return fmt.Errorf("%s failed to decode report metadata: %w", capcommon.UserError, err)
	}
	if reportMetadata.Version != 1 {
		return fmt.Errorf("%s unsupported report metadata version: %d", capcommon.UserError, reportMetadata.Version)
	}
	if reportMetadata.ExecutionID != metadata.WorkflowExecutionID {
		return fmt.Errorf("%s report workflowExecutionID does not match request metadata", capcommon.UserError)
	}
	if !strings.EqualFold(reportMetadata.WorkflowOwner, metadata.WorkflowOwner) {
		return fmt.Errorf("%s report workflowOwner does not match request metadata", capcommon.UserError)
	}
	expectedWorkflowName := metadata.WorkflowName
	if len(expectedWorkflowName) < 20 {
		expectedWorkflowName += strings.Repeat("0", 20-len(expectedWorkflowName))
	}
	if !strings.EqualFold(reportMetadata.WorkflowName, expectedWorkflowName) {
		return fmt.Errorf("%s report workflowName does not match request metadata", capcommon.UserError)
	}
	if reportMetadata.WorkflowID != metadata.WorkflowID {
		return fmt.Errorf("%s report workflowID does not match request metadata", capcommon.UserError)
	}
	return nil
}

func getTransmissionID(workflowExecutionID string, request *stellarcap.WriteReportRequest) ([32]byte, [32]byte, [2]byte, error) {
	rawExecutionID, reportID, err := capcommon.ParseTransmissionComponents(workflowExecutionID, request.Report.RawReport)
	if err != nil {
		return [32]byte{}, [32]byte{}, [2]byte{}, err
	}

	receiverBytes, err := strkey.Decode(strkey.VersionByteContract, request.ContractId)
	if err != nil {
		return [32]byte{}, [32]byte{}, [2]byte{}, fmt.Errorf("%s invalid receiver contract address: %w", capcommon.UserError, err)
	}

	hash := sha256.New()
	hash.Write(receiverBytes)
	hash.Write(rawExecutionID[:])
	hash.Write(reportID[:])

	var transmissionID [32]byte
	copy(transmissionID[:], hash.Sum(nil))
	return transmissionID, rawExecutionID, reportID, nil
}

func (wr *writeReport) pollTransmissionInfo(
	ctx context.Context,
	receiver string,
	workflowExecutionID [32]byte,
	reportID [2]byte,
	queuePosition int,
) (TransmissionInfo, error) {
	if queuePosition <= 0 {
		return capcommon.WithQuickRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
			return wr.getTransmissionInfo(ctx, receiver, workflowExecutionID, reportID)
		})
	}

	timer := time.NewTimer(time.Duration(queuePosition) * wr.transmissionScheduler.DeltaStage)
	defer timer.Stop()
	ticker := time.NewTicker(wr.transmissionScheduler.DeltaStage / 3)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return TransmissionInfo{}, ctx.Err()
		case <-ticker.C:
			info, err := wr.getTransmissionInfo(ctx, receiver, workflowExecutionID, reportID)
			if err != nil {
				wr.lggr.Debugw("failed to poll transmission info, retrying", "error", err)
				continue
			}
			if info.State != TransmissionStateNotAttempted {
				return info, nil
			}
		case <-timer.C:
			return wr.getTransmissionInfo(ctx, receiver, workflowExecutionID, reportID)
		}
	}
}

func (wr *writeReport) getTransmissionInfo(
	ctx context.Context,
	receiver string,
	workflowExecutionID [32]byte,
	reportID [2]byte,
) (TransmissionInfo, error) {
	args, err := buildTransmissionInfoArgs(receiver, workflowExecutionID, reportID)
	if err != nil {
		return TransmissionInfo{}, err
	}

	resp, err := wr.service.ReadContract(ctx, stellartypes.ReadContractRequest{
		ContractID: wr.forwarderAddress,
		Function:   forwarderGetTransmissionInfoFunction,
		Args:       args,
	})
	if err != nil {
		return TransmissionInfo{}, err
	}
	if resp.Error != "" {
		if strings.Contains(strings.ToLower(resp.Error), "missing") || strings.Contains(strings.ToLower(resp.Error), "not found") {
			return TransmissionInfo{State: TransmissionStateNotAttempted}, nil
		}
		return TransmissionInfo{}, fmt.Errorf("forwarder read failed: %s", resp.Error)
	}
	if resp.Result == "" {
		return TransmissionInfo{State: TransmissionStateNotAttempted}, nil
	}

	return parseTransmissionInfo(resp.Result, resp.LedgerSequence)
}

func parseTransmissionInfo(result string, ledgerSequence uint32) (TransmissionInfo, error) {
	var sv xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(result, &sv); err != nil {
		return TransmissionInfo{}, fmt.Errorf("decode transmission info result XDR: %w", err)
	}

	info := TransmissionInfo{State: TransmissionStateNotAttempted, LedgerSequence: ledgerSequence}
	parseFieldsIntoTransmissionInfo(&info, sv)
	info.Success = info.State == TransmissionStateSucceeded
	info.InvalidReceiver = info.State == TransmissionStateInvalidReceiver
	return info, nil
}

func parseFieldsIntoTransmissionInfo(info *TransmissionInfo, sv xdr.ScVal) {
	switch sv.Type {
	case xdr.ScValTypeScvU32:
		if sv.U32 != nil {
			info.State = TransmissionState(*sv.U32)
		}
	case xdr.ScValTypeScvI32:
		if sv.I32 != nil && *sv.I32 >= 0 {
			info.State = TransmissionState(*sv.I32)
		}
	case xdr.ScValTypeScvVec:
		if sv.Vec == nil || *sv.Vec == nil {
			return
		}
		vec := **sv.Vec
		if len(vec) > 0 {
			parseFieldsIntoTransmissionInfo(info, vec[0])
		}
		if len(vec) > 1 {
			if txr, ok := extractAddressString(vec[1]); ok {
				info.Transmitter = txr
			}
		}
	case xdr.ScValTypeScvMap:
		if sv.Map == nil || *sv.Map == nil {
			return
		}
		for _, entry := range **sv.Map {
			key, ok := extractStringish(entry.Key)
			if !ok {
				continue
			}
			switch strings.ToLower(key) {
			case "state":
				parseFieldsIntoTransmissionInfo(info, entry.Val)
			case "transmitter":
				if txr, ok := extractAddressString(entry.Val); ok {
					info.Transmitter = txr
				}
			case "success":
				if b := extractBool(entry.Val); b != nil {
					info.Success = *b
				}
			case "invalid_receiver", "invalidreceiver":
				if b := extractBool(entry.Val); b != nil {
					info.InvalidReceiver = *b
				}
			}
		}
	}
}

func buildForwarderReportArgs(receiver string, report *sdk.ReportResponse) ([]xdr.ScVal, error) {
	receiverAddr, err := contractIDToScAddress(receiver)
	if err != nil {
		return nil, err
	}

	signatures := make(xdr.ScVec, len(report.Sigs))
	for i, sig := range report.Sigs {
		s := xdr.ScBytes(sig.GetSignature())
		signatures[i] = xdr.ScVal{
			Type:  xdr.ScValTypeScvBytes,
			Bytes: &s,
		}
	}
	signaturesPtr := &signatures

	reportContext := xdr.ScBytes(report.GetReportContext())
	rawReport := xdr.ScBytes(report.GetRawReport())
	return []xdr.ScVal{
		{
			Type:    xdr.ScValTypeScvAddress,
			Address: receiverAddr,
		},
		{
			Type:  xdr.ScValTypeScvBytes,
			Bytes: &rawReport,
		},
		{
			Type:  xdr.ScValTypeScvBytes,
			Bytes: &reportContext,
		},
		{
			Type: xdr.ScValTypeScvVec,
			Vec:  &signaturesPtr,
		},
	}, nil
}

func buildTransmissionInfoArgs(receiver string, workflowExecutionID [32]byte, reportID [2]byte) ([]xdr.ScVal, error) {
	receiverAddr, err := contractIDToScAddress(receiver)
	if err != nil {
		return nil, err
	}
	execID := xdr.ScBytes(workflowExecutionID[:])
	reportIDU32 := uint32(binary.BigEndian.Uint16(reportID[:]))
	return []xdr.ScVal{
		{
			Type:    xdr.ScValTypeScvAddress,
			Address: receiverAddr,
		},
		{
			Type:  xdr.ScValTypeScvBytes,
			Bytes: &execID,
		},
		{
			Type: xdr.ScValTypeScvU32,
			U32:  &reportIDU32,
		},
	}, nil
}

func buildInvokeContractOperationsXDR(contractID string, functionName string, args []xdr.ScVal) ([]string, error) {
	contractAddr, err := contractIDToScAddress(contractID)
	if err != nil {
		return nil, err
	}

	opBody := xdr.OperationBody{}
	opBody.Type = xdr.OperationTypeInvokeHostFunction
	opBody.InvokeHostFunctionOp = &xdr.InvokeHostFunctionOp{
		HostFunction: xdr.HostFunction{
			Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
			InvokeContract: &xdr.InvokeContractArgs{
				ContractAddress: *contractAddr,
				FunctionName:    xdr.ScSymbol(functionName),
				Args:            args,
			},
		},
		Auth: nil,
	}

	op := xdr.Operation{Body: opBody}
	opXDR, err := xdr.MarshalBase64(op)
	if err != nil {
		return nil, fmt.Errorf("marshal operation XDR: %w", err)
	}
	return []string{opXDR}, nil
}

func contractIDToScAddress(contractID string) (*xdr.ScAddress, error) {
	contractBytes, err := strkey.Decode(strkey.VersionByteContract, contractID)
	if err != nil {
		return nil, fmt.Errorf("%s invalid contract address %q: %w", capcommon.UserError, contractID, err)
	}
	var contractHash xdr.Hash
	copy(contractHash[:], contractBytes)
	addr, err := xdr.NewScAddress(xdr.ScAddressTypeScAddressTypeContract, xdr.ContractId(contractHash))
	if err != nil {
		return nil, fmt.Errorf("create contract sc address: %w", err)
	}
	return &addr, nil
}

func extractStringish(sv xdr.ScVal) (string, bool) {
	switch sv.Type {
	case xdr.ScValTypeScvSymbol:
		if sv.Sym == nil {
			return "", false
		}
		return string(*sv.Sym), true
	case xdr.ScValTypeScvString:
		if sv.Str == nil {
			return "", false
		}
		return string(*sv.Str), true
	default:
		return "", false
	}
}

func extractAddressString(sv xdr.ScVal) (string, bool) {
	if sv.Type != xdr.ScValTypeScvAddress || sv.Address == nil {
		return "", false
	}
	switch sv.Address.Type {
	case xdr.ScAddressTypeScAddressTypeAccount:
		if sv.Address.AccountId == nil {
			return "", false
		}
		raw := sv.Address.AccountId.Ed25519
		out, err := strkey.Encode(strkey.VersionByteAccountID, raw[:])
		return out, err == nil
	case xdr.ScAddressTypeScAddressTypeContract:
		if sv.Address.ContractId == nil {
			return "", false
		}
		raw := *sv.Address.ContractId
		out, err := strkey.Encode(strkey.VersionByteContract, raw[:])
		return out, err == nil
	default:
		return "", false
	}
}

func extractBool(sv xdr.ScVal) *bool {
	if sv.Type != xdr.ScValTypeScvBool || sv.B == nil {
		return nil
	}
	b := bool(*sv.B)
	return &b
}

func (wr *writeReport) successReplyFromObservedState(info TransmissionInfo) *stellarcap.WriteReportReply {
	reply := &stellarcap.WriteReportReply{
		TxStatus: stellarcap.TxStatus_TX_STATUS_SUCCESS,
	}
	status := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS
	reply.ReceiverContractExecutionStatus = &status
	if info.LedgerSequence != 0 {
		reply.LedgerSequence = capcommon.Ptr(info.LedgerSequence)
	}
	return reply
}

func (wr *writeReport) revertedReplyFromObservedState(info TransmissionInfo) *stellarcap.WriteReportReply {
	reply := &stellarcap.WriteReportReply{
		TxStatus: stellarcap.TxStatus_TX_STATUS_REVERTED,
	}
	status := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED
	reply.ReceiverContractExecutionStatus = &status
	if info.LedgerSequence != 0 {
		reply.LedgerSequence = capcommon.Ptr(info.LedgerSequence)
	}
	return reply
}

func (wr *writeReport) replyFromOwnTransaction(txHash string, txResult stellartypes.TxResult) *stellarcap.WriteReportReply {
	reply := &stellarcap.WriteReportReply{}
	populateReplyFromTx(reply, txHash, txResult)
	if txResult.Status == int32(types.Finalized) {
		reply.TxStatus = stellarcap.TxStatus_TX_STATUS_SUCCESS
		status := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS
		reply.ReceiverContractExecutionStatus = &status
	} else if txResult.Status == int32(types.Failed) {
		reply.TxStatus = stellarcap.TxStatus_TX_STATUS_REVERTED
		status := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED
		reply.ReceiverContractExecutionStatus = &status
	} else {
		reply.TxStatus = stellarcap.TxStatus_TX_STATUS_FATAL
	}
	return reply
}

func populateReplyFromTx(reply *stellarcap.WriteReportReply, txHash string, txResult stellartypes.TxResult) {
	if txHash != "" {
		reply.TxHash = capcommon.Ptr(txHash)
	} else if txResult.Hash != "" {
		reply.TxHash = capcommon.Ptr(txResult.Hash)
	}
	if txResult.Fee > 0 {
		fee := uint64(txResult.Fee)
		reply.TransactionFee = &fee
	}
	if txResult.ResultMetaXDR != "" {
		if ledgerSequence, err := extractLedgerSequenceFromResultMeta(txResult.ResultMetaXDR); err == nil && ledgerSequence != 0 {
			reply.LedgerSequence = &ledgerSequence
		}
	}
}

func extractLedgerSequenceFromResultMeta(resultMetaXDR string) (uint32, error) {
	var meta xdr.TransactionMeta
	if err := xdr.SafeUnmarshalBase64(resultMetaXDR, &meta); err != nil {
		return 0, err
	}
	switch meta.V {
	case 4:
		v := meta.MustV4()
		if v.SorobanMeta == nil || v.SorobanMeta.Ext.V != 1 || v.SorobanMeta.Ext.Events == nil {
			return 0, nil
		}
		return 0, nil
	case 3:
		return 0, nil
	default:
		return 0, nil
	}
}
