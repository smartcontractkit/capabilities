package actions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	types "github.com/smartcontractkit/chainlink-common/pkg/types"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/metering"
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
	nodeAddress           string
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
		return nil, NewUserError(err, caperrors.InvalidArgument)
	}

	wr := &writeReport{
		service:               s.StellarService,
		lggr:                  s.lggr,
		forwarderAddress:      s.forwarderAddress,
		nodeAddress:           s.nodeAddress,
		chainSelector:         s.chainSelector,
		reportSizeLimit:       s.reportSizeLimit,
		transmissionScheduler: s.transmissionScheduler,
	}

	reply, err := wr.execute(ctx, metadata, input)
	if err != nil {
		return nil, GetError(err, s.isUserErrorWriteReport(err))
	}

	var meteringMeta capabilities.ResponseMetadata
	if reply.TransactionFee != nil {
		meteringMeta = metering.GetResponseMetadataWriteReport(*reply.TransactionFee, s.chainSelector)
	}

	return &capabilities.ResponseAndMetadata[*stellarcap.WriteReportReply]{
		Response:         reply,
		ResponseMetadata: meteringMeta,
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
		return wr.revertedReplyFromObservedState(info, "receiver contract cannot accept reports: not a Wasm contract or missing on_report function"), nil
	case TransmissionStateFailed:
		return wr.revertedReplyFromObservedState(info, "receiver contract execution failed"), nil
	case TransmissionStateNotAttempted:
	default:
		return nil, fmt.Errorf("unexpected transmission state: %d", info.State)
	}

	if err := wr.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport)); err != nil {
		return nil, fmt.Errorf("%s report size exceeds limit: %w", capcommon.UserError, err)
	}

	if wr.nodeAddress == "" {
		return nil, fmt.Errorf("%s node address is not configured; required for write operations", capcommon.UserError)
	}

	args, err := buildForwarderReportArgs(wr.nodeAddress, request.ContractId, request.Report)
	if err != nil {
		return nil, err
	}

	// Pre-check the report against the forwarder via simulation (ReadContract uses simulate-only;
	// no signing, no fee). This catches forwarder-level user errors (wrong DON, bad report
	// metadata, invalid config) cheaply before committing to TXM submission.
	simResp, err := wr.service.ReadContract(ctx, stellartypes.ReadContractRequest{
		ContractID: wr.forwarderAddress,
		Function:   forwarderReportFunction,
		Args:       args,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to simulate forwarder report call: %w", err)
	}
	if simResp.Error != "" {
		return nil, fmt.Errorf("%s forwarder simulation failed: %s", capcommon.UserError, simResp.Error)
	}

	// Submit via TXM, which handles simulate-for-fees, signing, sending, and on-chain confirmation.
	txID := uuid.NewString()
	submitResp, err := wr.service.SubmitTransaction(ctx, stellartypes.SubmitTransactionRequest{
		IdempotencyKey:     txID,
		FromAddress:        wr.nodeAddress,
		ContractID:         wr.forwarderAddress,
		Function:           forwarderReportFunction,
		Args:               args,
		LedgerBoundsOffset: defaultLedgerBoundsOffset,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to submit forwarder report transaction: %w", err)
	}

	// Poll for the canonical on-chain transmission state. The forwarder may record the
	// outcome after the tx confirms; retry until it is visible or the context expires.
	postInfo, pollErr := capcommon.WithPollingRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
		readInfo, readErr := wr.getTransmissionInfo(ctx, request.ContractId, workflowExecutionID, reportID)
		if readErr != nil {
			return TransmissionInfo{}, readErr
		}
		if readInfo.State == TransmissionStateNotAttempted {
			return TransmissionInfo{}, errors.New("tx submitted but transmission info not yet visible on-chain, retrying")
		}
		return readInfo, nil
	})
	if pollErr != nil {
		// TX was submitted but on-chain state is still not visible; use the TXM result directly.
		wr.lggr.Warnw("Failed to poll transmission info after submit, returning reply from TX outcome", "error", pollErr)
		return wr.replyFromOwnTransaction(submitResp), nil
	}

	switch postInfo.State {
	case TransmissionStateSucceeded:
		reply := wr.successReplyFromObservedState(postInfo)
		populateReplyFromSubmit(reply, submitResp)
		return reply, nil
	case TransmissionStateInvalidReceiver:
		reply := wr.revertedReplyFromObservedState(postInfo, "receiver contract cannot accept reports: not a Wasm contract or missing on_report function")
		populateReplyFromSubmit(reply, submitResp)
		return reply, nil
	case TransmissionStateFailed:
		reply := wr.revertedReplyFromObservedState(postInfo, "receiver contract execution failed")
		populateReplyFromSubmit(reply, submitResp)
		return reply, nil
	default:
		return wr.replyFromOwnTransaction(submitResp), nil
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
		return errors.New("contracId is required")
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
	default:
	}
}

// buildForwarderReportArgs constructs the domain ScVal argument list for the forwarder report() function.
//
// Arg order matches the on-chain Rust signature:
//
//	report(transmitter: Address, receiver: Address, raw_report: Bytes, report_context: Bytes, signatures: Vec<BytesN<65>>)
func buildForwarderReportArgs(transmitter, receiver string, report *sdk.ReportResponse) ([]stellartypes.ScVal, error) {
	transmitterVal, err := accountAddressToScVal(transmitter)
	if err != nil {
		return nil, fmt.Errorf("transmitter: %w", err)
	}

	receiverVal, err := contractAddressToScVal(receiver)
	if err != nil {
		return nil, err
	}

	rawReportVal := stellartypes.ScVal{Type: stellartypes.ScValTypeBytes, Bytes: report.GetRawReport()}
	reportContextVal := stellartypes.ScVal{Type: stellartypes.ScValTypeBytes, Bytes: report.GetReportContext()}

	sigVals := make([]*stellartypes.ScVal, len(report.Sigs))
	for i, sig := range report.Sigs {
		s := stellartypes.ScVal{Type: stellartypes.ScValTypeBytes, Bytes: sig.GetSignature()}
		sigVals[i] = &s
	}
	sigsVal := stellartypes.ScVal{
		Type: stellartypes.ScValTypeVec,
		Vec:  &stellartypes.ScVec{Values: sigVals},
	}

	return []stellartypes.ScVal{transmitterVal, receiverVal, rawReportVal, reportContextVal, sigsVal}, nil
}

// buildTransmissionInfoArgs constructs the domain ScVal argument list for get_transmission_info().
//
// Arg order matches the on-chain Rust signature:
//
//	get_transmission_info(receiver: Address, workflow_execution_id: BytesN<32>, report_id: BytesN<2>)
func buildTransmissionInfoArgs(receiver string, workflowExecutionID [32]byte, reportID [2]byte) ([]stellartypes.ScVal, error) {
	receiverVal, err := contractAddressToScVal(receiver)
	if err != nil {
		return nil, err
	}
	return []stellartypes.ScVal{
		receiverVal,
		{Type: stellartypes.ScValTypeBytes, Bytes: workflowExecutionID[:]},
		// report_id is BytesN<2> on-chain — pass as raw bytes, not as a uint32.
		{Type: stellartypes.ScValTypeBytes, Bytes: reportID[:]},
	}, nil
}

// contractAddressToScVal converts a C... StrKey contract address to a domain ScVal of type Address.
func contractAddressToScVal(contractID string) (stellartypes.ScVal, error) {
	contractBytes, err := strkey.Decode(strkey.VersionByteContract, contractID)
	if err != nil {
		return stellartypes.ScVal{}, fmt.Errorf("%s invalid contract address %q: %w", capcommon.UserError, contractID, err)
	}
	if len(contractBytes) != 32 {
		return stellartypes.ScVal{}, fmt.Errorf("%s contract address must decode to 32 bytes, got %d", capcommon.UserError, len(contractBytes))
	}
	return stellartypes.ScVal{
		Type: stellartypes.ScValTypeAddress,
		Address: &stellartypes.ScAddress{
			Type:       stellartypes.ScAddressTypeContractID,
			ContractID: contractBytes,
		},
	}, nil
}

// accountAddressToScVal converts a G... StrKey account address to a domain ScVal of type Address.
func accountAddressToScVal(accountID string) (stellartypes.ScVal, error) {
	accountBytes, err := strkey.Decode(strkey.VersionByteAccountID, accountID)
	if err != nil {
		return stellartypes.ScVal{}, fmt.Errorf("invalid account address %q: %w", accountID, err)
	}
	if len(accountBytes) != 32 {
		return stellartypes.ScVal{}, fmt.Errorf("account address must decode to 32 bytes, got %d", len(accountBytes))
	}
	return stellartypes.ScVal{
		Type: stellartypes.ScValTypeAddress,
		Address: &stellartypes.ScAddress{
			Type:      stellartypes.ScAddressTypeAccountID,
			AccountID: accountBytes,
		},
	}, nil
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
	b := *sv.B
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

func (wr *writeReport) revertedReplyFromObservedState(info TransmissionInfo, errorMsg string) *stellarcap.WriteReportReply {
	reply := &stellarcap.WriteReportReply{
		TxStatus: stellarcap.TxStatus_TX_STATUS_REVERTED,
	}
	status := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED
	reply.ReceiverContractExecutionStatus = &status
	if info.LedgerSequence != 0 {
		reply.LedgerSequence = capcommon.Ptr(info.LedgerSequence)
	}
	if errorMsg != "" {
		reply.ErrorMessage = capcommon.Ptr(errorMsg)
	}
	return reply
}

// replyFromOwnTransaction builds a WriteReportReply directly from a SubmitTransactionResponse
// when the post-submit transmission info poll fails or returns an unexpected state.
func (wr *writeReport) replyFromOwnTransaction(resp *stellartypes.SubmitTransactionResponse) *stellarcap.WriteReportReply {
	reply := &stellarcap.WriteReportReply{}
	if resp == nil {
		reply.TxStatus = stellarcap.TxStatus_TX_STATUS_FATAL
		return reply
	}
	populateReplyFromSubmit(reply, resp)
	switch resp.TxStatus {
	case stellartypes.TxSuccess:
		reply.TxStatus = stellarcap.TxStatus_TX_STATUS_SUCCESS
		status := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS
		reply.ReceiverContractExecutionStatus = &status
	case stellartypes.TxFailed:
		reply.TxStatus = stellarcap.TxStatus_TX_STATUS_REVERTED
		status := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED
		reply.ReceiverContractExecutionStatus = &status
		if resp.Error != "" {
			reply.ErrorMessage = capcommon.Ptr("on-chain transaction failed: " + resp.Error)
		}
	default: // TxFatal
		reply.TxStatus = stellarcap.TxStatus_TX_STATUS_FATAL
	}
	return reply
}

// populateReplyFromSubmit sets tx hash, ledger sequence, and fee on the reply from a SubmitTransactionResponse.
func populateReplyFromSubmit(reply *stellarcap.WriteReportReply, resp *stellartypes.SubmitTransactionResponse) {
	if resp == nil {
		return
	}
	if resp.TxHash != "" {
		reply.TxHash = capcommon.Ptr(resp.TxHash)
	}
	if resp.TransactionFee != nil {
		reply.TransactionFee = resp.TransactionFee
	}
	if resp.ResultMetaXDR != "" {
		if ledgerSequence, err := extractLedgerSequenceFromResultMeta(resp.ResultMetaXDR); err == nil && ledgerSequence != 0 {
			reply.LedgerSequence = &ledgerSequence
		}
	}
}

// extractLedgerSequenceFromResultMeta parses the ledger sequence from transaction result meta XDR.
// The Stellar RPC does not embed the ledger sequence directly in TransactionMeta; it is returned
// separately in the GetTransaction response, which is available via the on-chain transmission info.
// This function is a best-effort helper and returns 0 when the ledger cannot be determined.
func extractLedgerSequenceFromResultMeta(_ string) (uint32, error) {
	return 0, nil
}
