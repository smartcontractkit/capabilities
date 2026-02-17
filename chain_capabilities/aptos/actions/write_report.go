package actions

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

// WriteReportRequest is the input to the WriteReport capability action.
// TODO: Replace with a proto-generated type once the Aptos capability proto is defined.
type WriteReportRequest struct {
	Receiver [32]byte            // 32-byte Aptos receiver module address
	Report   *sdk.ReportResponse // Signed report from consensus
}

// WriteReportReply is the response from a WriteReport call.
// TODO: Replace with a proto-generated type once the Aptos capability proto is defined.
type WriteReportReply struct {
	TxHash       string
	TxStatus     TxStatus
	ErrorMessage *string
}

// TxStatus represents the status of a transaction.
type TxStatus int32

const (
	TxStatusUnspecified TxStatus = 0
	TxStatusSuccess     TxStatus = 1
	TxStatusAborted     TxStatus = 2
	TxStatusFatal       TxStatus = 3
)

// WriteReport validates and submits a signed report to the Aptos chain via the CRE forwarder.
// It handles only the simple successful case for now.
func (s *Aptos) WriteReport(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.WriteReportRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.WriteReportReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	// 1. Validate inputs
	if err := s.validateWriteReportInputs(metadata, input); err != nil {
		return nil, NewUserError(err)
	}

	// 2. Build and submit the transaction via AptosService
	reply, err := s.executeWriteReport(ctx, input, metadata)
	if err != nil {
		return nil, GetError(err, s.isUserError(err))
	}

	return &capabilities.ResponseAndMetadata[*aptoscap.WriteReportReply]{
		Response:         reply,
		ResponseMetadata: capabilities.ResponseMetadata{},
	}, nil
}

func (s *Aptos) executeWriteReport(
	ctx context.Context,
	request *aptoscap.WriteReportRequest,
	metadata capabilities.RequestMetadata,
) (*aptoscap.WriteReportReply, error) {
	receiver := request.Receiver
	report := request.Report

	// Build the payload for the forwarder's report entry function.
	// Format: [num_sigs (1 byte)] + [signatures...] + [raw_report] + [report_context]
	payload := buildForwarderPayload(report)

	s.lggr.Debugw("Submitting WriteReport transaction",
		"executionID", metadata.WorkflowExecutionID,
		"receiver", hex.EncodeToString(receiver[:]),
	)

	txReply, err := s.aptosService.SubmitTransaction(ctx, aptostypes.SubmitTransactionRequest{
		ReceiverModuleID: aptostypes.ModuleID{
			Address: aptostypes.AccountAddress(s.forwarderAddress),
			Name:    "forwarder",
		},
		EncodedPayload: payload,
		GasConfig:      nil, // Use default gas config
	})
	if err != nil {
		return nil, fmt.Errorf("failed to submit transaction: %w", err)
	}

	if txReply == nil || txReply.PendingTransaction == nil {
		return nil, fmt.Errorf("nil transaction reply")
	}

	s.lggr.Infow("WriteReport succeeded",
		"executionID", metadata.WorkflowExecutionID,
		"txHash", txReply.PendingTransaction.Hash,
	)

	return &aptoscap.WriteReportReply{
		TxStatus: aptoscap.TxStatus_TX_STATUS_SUCCESS,
		TxHash:   &txReply.PendingTransaction.Hash,
	}, nil
}

func (s *Aptos) validateWriteReportInputs(requestMetadata capabilities.RequestMetadata, request *aptoscap.WriteReportRequest) error {
	if request == nil {
		return fmt.Errorf("nil WriteReportRequest")
	}
	if request.Report == nil {
		return fmt.Errorf("nil Report in WriteReportRequest")
	}
	if len(request.Report.Sigs) == 0 {
		return fmt.Errorf("no signatures provided")
	}

	reportMetadata, err := decodeReportMetadata(request.Report.RawReport)
	if err != nil {
		return err
	}

	if reportMetadata.Version != 1 {
		return fmt.Errorf("unsupported report version: %d", reportMetadata.Version)
	}

	if reportMetadata.ExecutionID != requestMetadata.WorkflowExecutionID {
		return fmt.Errorf("workflowExecutionID mismatch: report=%s, request=%s",
			reportMetadata.ExecutionID, requestMetadata.WorkflowExecutionID)
	}

	if !strings.EqualFold(reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner) {
		return fmt.Errorf("workflowOwner mismatch: report=%s, request=%s",
			reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner)
	}

	if reportMetadata.WorkflowID != requestMetadata.WorkflowID {
		return fmt.Errorf("workflowID mismatch: report=%s, request=%s",
			reportMetadata.WorkflowID, requestMetadata.WorkflowID)
	}

	return nil
}

func decodeReportMetadata(data []byte) (ocrtypes.Metadata, error) {
	metadata, _, err := ocrtypes.Decode(data)
	return metadata, err
}

// buildForwarderPayload builds the payload bytes sent to the Aptos forwarder.
// Format: [num_sigs (1 byte)] + [signatures...] + [raw_report] + [report_context]
// This mirrors the Solana forwarder payload format.
func buildForwarderPayload(report *sdk.ReportResponse) []byte {
	var payload []byte

	// 1. Number of signatures
	payload = append(payload, byte(len(report.Sigs)))

	// 2. Signatures
	for _, sig := range report.Sigs {
		payload = append(payload, sig.Signature...)
	}

	// 3. Raw report
	payload = append(payload, report.RawReport...)

	// 4. Report context
	payload = append(payload, report.ReportContext...)

	return payload
}

func (s *Aptos) isUserError(err error) bool {
	return strings.HasPrefix(err.Error(), "user error:")
}
