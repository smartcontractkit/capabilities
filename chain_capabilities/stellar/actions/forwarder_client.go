package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
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

// CREForwarderClient abstracts interaction with the Stellar CRE forwarder contract.
type CREForwarderClient interface {
	// InvokeOnReport resolves the relayer signing account, builds forwarder args, and submits via TXM.
	InvokeOnReport(ctx context.Context, receiver string, report *sdk.ReportResponse) (*stellartypes.SubmitTransactionResponse, error)
	// GetTransmissionInfo queries the forwarder for transmission state.
	GetTransmissionInfo(ctx context.Context, receiver string, workflowExecutionID [32]byte, reportID [2]byte) (TransmissionInfo, error)
	GetReportProcessedEvents(ctx context.Context, receiver string, workflowExecutionID [32]byte, reportID [2]byte) ([]ReportProcessedEvent, error)
	ForwarderAddress() string
}

type forwarderClient struct {
	types.StellarService
	lggr                     logger.Logger
	forwarderCodec           CREForwarderCodec
	forwarderAddress         string
	forwarderLookbackLedgers int64
}

type ReportProcessedEvent struct {
	TxHash  string
	Ledger  uint32
	Success bool
}

func newForwarderClient(service types.StellarService, lggr logger.Logger, forwarderAddress string, forwarderLookbackLedgers int64) CREForwarderClient {
	if forwarderLookbackLedgers <= 0 {
		forwarderLookbackLedgers = defaultForwarderLookbackLedgers
	}
	return &forwarderClient{
		StellarService:           service,
		lggr:                     logger.Named(lggr, "ForwarderClient"),
		forwarderCodec:           NewCREForwarderCodec(),
		forwarderAddress:         forwarderAddress,
		forwarderLookbackLedgers: forwarderLookbackLedgers,
	}
}

func (fc *forwarderClient) ForwarderAddress() string {
	return fc.forwarderAddress
}

func (fc *forwarderClient) InvokeOnReport(
	ctx context.Context,
	receiver string,
	report *sdk.ReportResponse,
) (*stellartypes.SubmitTransactionResponse, error) {
	transmitter, err := fc.resolveSigningAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve signing account: %w", err)
	}

	args, err := fc.forwarderCodec.EncodeReport(transmitter, receiver, report)
	if err != nil {
		return nil, err
	}

	submitResp, err := fc.SubmitTransaction(ctx, stellartypes.SubmitTransactionRequest{
		ContractID:         fc.forwarderAddress,
		Function:           forwarderReportFunction,
		Args:               args,
		LedgerBoundsOffset: defaultLedgerBoundsOffset,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to submit forwarder report transaction: %w", err)
	}
	return submitResp, nil
}

func (fc *forwarderClient) GetTransmissionInfo(
	ctx context.Context,
	receiver string,
	workflowExecutionID [32]byte,
	reportID [2]byte,
) (TransmissionInfo, error) {
	args, err := fc.forwarderCodec.EncodeQueryTransmissionInputs(QueryTransmissionInputs{
		Receiver:            receiver,
		WorkflowExecutionID: workflowExecutionID,
		ReportID:            reportID,
	})
	if err != nil {
		return TransmissionInfo{}, err
	}

	resp, err := fc.SimulateTransaction(ctx, stellartypes.SimulateTransactionRequest{
		ContractID: fc.forwarderAddress,
		Function:   forwarderGetTransmissionInfoFunction,
		Args:       args,
	})
	if err != nil {
		return TransmissionInfo{}, err
	}
	if resp.Error != "" {
		return TransmissionInfo{}, fmt.Errorf("forwarder simulation failed: %s", resp.Error)
	}
	if resp.ReturnValueXDR == "" {
		return TransmissionInfo{State: TransmissionStateNotAttempted}, nil
	}

	return fc.forwarderCodec.DecodeQueryTransmissionInfo(resp.ReturnValueXDR, resp.LedgerSequence)
}

func (fc *forwarderClient) GetReportProcessedEvents(
	ctx context.Context,
	receiver string,
	workflowExecutionID [32]byte,
	reportID [2]byte,
) ([]ReportProcessedEvent, error) {
	startLedger, err := fc.startLedger(ctx)
	if err != nil {
		return nil, err
	}
	topicFilter, err := fc.forwarderCodec.EncodeReportProcessedTopicFilter(receiver, workflowExecutionID, reportID)
	if err != nil {
		return nil, err
	}

	resp, err := fc.GetEvents(ctx, stellartypes.GetEventsRequest{
		StartLedger: startLedger,
		Filters: []stellartypes.EventFilter{
			{
				EventTypes:  []stellartypes.EventType{stellartypes.EventTypeContract},
				ContractIDs: []string{fc.forwarderAddress},
				Topics:      []stellartypes.TopicFilter{topicFilter},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	events := make([]ReportProcessedEvent, 0, len(resp.Events))
	for i, e := range resp.Events {
		if e.TransactionHash == "" {
			return nil, fmt.Errorf("empty tx hash at event index %d", i)
		}
		if e.Value.Type != stellartypes.ScValTypeBool || e.Value.Bool == nil {
			return nil, fmt.Errorf("event %s value is not a bool", e.TransactionHash)
		}
		events = append(events, ReportProcessedEvent{
			TxHash:  e.TransactionHash,
			Ledger:  e.Ledger,
			Success: *e.Value.Bool,
		})
	}
	return events, nil
}

func (fc *forwarderClient) startLedger(ctx context.Context) (uint32, error) {
	latest, err := fc.GetLatestLedger(ctx)
	if err != nil {
		return 0, err
	}
	if int64(latest.Sequence) <= fc.forwarderLookbackLedgers {
		return 1, nil
	}
	start := int64(latest.Sequence) - fc.forwarderLookbackLedgers
	return uint32(start), nil //nolint:gosec // G115: start is positive and at most latest.Sequence (uint32)
}

func (fc *forwarderClient) resolveSigningAccount(ctx context.Context) (string, error) {
	resp, err := fc.GetSigningAccount(ctx)
	if err != nil {
		return "", err
	}
	if resp.AccountAddress == "" {
		return "", errors.New("relayer returned empty signing account")
	}
	return resp.AccountAddress, nil
}
