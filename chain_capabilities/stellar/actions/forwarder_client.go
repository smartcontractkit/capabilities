package actions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
)

const (
	forwarderReportFunction              = "report"
	forwarderGetTransmissionInfoFunction = "get_transmission_info"
	defaultLedgerBoundsOffset            = uint32(20)
	reportProcessedEventPageLimit        = uint32(100)
	reportProcessedEventMaxPages         = 10
	// DefaultForwarderLookbackLedgers is how many ledgers back to search for ReportProcessed events.
	DefaultForwarderLookbackLedgers = int64(100)
)

type TransmissionState uint32

const (
	TransmissionStateNotAttempted TransmissionState = iota
	TransmissionStateSucceeded
	TransmissionStateInvalidReceiver
	TransmissionStateFailed
	TransmissionStateUnknown
)

type TransmissionInfo struct {
	State           TransmissionState
	Transmitter     string
	LedgerSequence  uint32
	Success         bool
	InvalidReceiver bool
}

type EventSearchRange struct {
	StartLedger uint32
	EndLedger   uint32
}

// TransmissionID uniquely identifies a forwarder transmission (receiver + report components).
type TransmissionID struct {
	Receiver            string
	WorkflowExecutionID [32]byte
	ReportID            [2]byte
}

func (t TransmissionID) ReportIDHex() string {
	return hex.EncodeToString(t.ReportID[:])
}

func (t TransmissionID) WorkflowExecutionIDHex() string {
	return hex.EncodeToString(t.WorkflowExecutionID[:])
}

func (t TransmissionID) InvalidReceiverMessage() string {
	return "receiver contract cannot accept reports: not a Wasm contract or missing on_report function"
}

// LogAttrs returns compact fields for structured logging.
func (t TransmissionID) LogAttrs() []any {
	return []any{
		"receiver", t.Receiver,
		"reportID", t.ReportIDHex(),
		"workflowExecutionID", t.WorkflowExecutionIDHex(),
	}
}

func (t TransmissionID) idempotencyKey() string {
	return fmt.Sprintf("report:%s:%s:%s", t.Receiver, t.WorkflowExecutionIDHex(), t.ReportIDHex())
}

// ScheduleKey returns the SHA-256 key used to seed the transmission schedule permutation.
func (t TransmissionID) ScheduleKey() ([32]byte, error) {
	receiverBytes, err := strkey.Decode(strkey.VersionByteContract, t.Receiver)
	if err != nil {
		return [32]byte{}, fmt.Errorf("%s invalid receiver contract address: %w", capcommon.UserError, err)
	}

	hash := sha256.New()
	hash.Write(receiverBytes)
	hash.Write(t.WorkflowExecutionID[:])
	hash.Write(t.ReportID[:])

	var scheduleKey [32]byte
	copy(scheduleKey[:], hash.Sum(nil))
	return scheduleKey, nil
}

// CREForwarderClient abstracts interaction with the Stellar CRE forwarder contract.
type CREForwarderClient interface {
	ResolveSigningAccount(ctx context.Context) (string, error)
	InvokeOnReport(ctx context.Context, transmitter, receiver string, report *sdk.ReportResponse, transmissionID TransmissionID, maxResourceFee uint64) (*stellartypes.SubmitTransactionResponse, error)
	SimulateReport(ctx context.Context, transmitter, receiver string, report *sdk.ReportResponse) (stellartypes.SimulateTransactionResponse, error)
	ValidateReportSimulation(simResp stellartypes.SimulateTransactionResponse, transmissionID TransmissionID) error
	// GetTransmissionInfo queries the forwarder for transmission state.
	GetTransmissionInfo(ctx context.Context, transmissionID TransmissionID) (TransmissionInfo, error)
	GetReportProcessedEventSearchRange(ctx context.Context) (EventSearchRange, error)
	GetReportProcessedEventSearchEndLedger(ctx context.Context) (uint32, error)
	GetReportProcessedEvents(ctx context.Context, transmissionID TransmissionID, searchRange EventSearchRange) ([]ReportProcessedEvent, error)
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
		forwarderLookbackLedgers = DefaultForwarderLookbackLedgers
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

func (fc *forwarderClient) ResolveSigningAccount(ctx context.Context) (string, error) {
	return fc.resolveSigningAccount(ctx)
}

func (fc *forwarderClient) InvokeOnReport(
	ctx context.Context,
	transmitter, receiver string,
	report *sdk.ReportResponse,
	transmissionID TransmissionID,
	maxResourceFee uint64,
) (*stellartypes.SubmitTransactionResponse, error) {
	args, err := fc.forwarderCodec.EncodeReport(transmitter, receiver, report)
	if err != nil {
		return nil, err
	}

	submitResp, err := fc.SubmitTransaction(ctx, stellartypes.SubmitTransactionRequest{
		ContractID:         fc.forwarderAddress,
		Function:           forwarderReportFunction,
		Args:               args,
		FromAddress:        transmitter,
		IdempotencyKey:     transmissionID.idempotencyKey(),
		MaxResourceFee:     maxResourceFee,
		LedgerBoundsOffset: defaultLedgerBoundsOffset,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to submit forwarder report transaction: %w", err)
	}
	return submitResp, nil
}

// SimulateReport simulates the same report() invocation InvokeOnReport submits.
func (fc *forwarderClient) SimulateReport(
	ctx context.Context,
	transmitter, receiver string,
	report *sdk.ReportResponse,
) (stellartypes.SimulateTransactionResponse, error) {
	args, err := fc.forwarderCodec.EncodeReport(transmitter, receiver, report)
	if err != nil {
		return stellartypes.SimulateTransactionResponse{}, err
	}

	resp, err := fc.SimulateTransaction(ctx, stellartypes.SimulateTransactionRequest{
		ContractID:    fc.forwarderAddress,
		Function:      forwarderReportFunction,
		Args:          args,
		SourceAccount: transmitter,
	})
	if err != nil {
		return stellartypes.SimulateTransactionResponse{}, fmt.Errorf("failed to simulate forwarder report transaction: %w", err)
	}
	return resp, nil
}

// ValidateReportSimulation requires the simulated ReportProcessed event to report success.
func (fc *forwarderClient) ValidateReportSimulation(simResp stellartypes.SimulateTransactionResponse, transmissionID TransmissionID) error {
	if len(simResp.EventsXDR) == 0 {
		return fmt.Errorf("pre-submit report simulation emitted no ReportProcessed event; receiver outcome cannot be confirmed")
	}
	for _, eventXDR := range simResp.EventsXDR {
		success, err := fc.forwarderCodec.DecodeReportProcessedEvent(eventXDR, fc.forwarderAddress, transmissionID)
		if err != nil {
			// Not the matching ReportProcessed event.
			continue
		}
		if !success {
			return fmt.Errorf("pre-submit report simulation indicated receiver cannot accept the report: ReportProcessed(success=false)")
		}
		return nil
	}
	return fmt.Errorf("pre-submit report simulation emitted no ReportProcessed event matching this transmission; receiver outcome cannot be confirmed")
}

func (fc *forwarderClient) GetTransmissionInfo(
	ctx context.Context,
	transmissionID TransmissionID,
) (TransmissionInfo, error) {
	args, err := fc.forwarderCodec.EncodeQueryTransmissionInputs(transmissionID)
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
		// Empty state is unknown, not spend-authorizing NotAttempted.
		return TransmissionInfo{State: TransmissionStateUnknown}, nil
	}

	return fc.forwarderCodec.DecodeQueryTransmissionInfo(resp.ReturnValueXDR, resp.LedgerSequence)
}

func (fc *forwarderClient) GetReportProcessedEvents(
	ctx context.Context,
	transmissionID TransmissionID,
	searchRange EventSearchRange,
) ([]ReportProcessedEvent, error) {
	if searchRange.StartLedger == 0 {
		return nil, errors.New("event search start ledger is required")
	}
	if searchRange.EndLedger == 0 {
		return nil, errors.New("event search end ledger is required")
	}
	if searchRange.StartLedger > searchRange.EndLedger {
		return nil, fmt.Errorf("invalid event search range: start ledger %d is after end ledger %d", searchRange.StartLedger, searchRange.EndLedger)
	}

	topicFilter, err := fc.forwarderCodec.EncodeReportProcessedTopicFilter(transmissionID)
	if err != nil {
		return nil, err
	}

	var events []ReportProcessedEvent
	cursor := ""
	for page := 0; page < reportProcessedEventMaxPages; page++ {
		resp, err := fc.GetEvents(ctx, stellartypes.GetEventsRequest{
			StartLedger: searchRange.StartLedger,
			EndLedger:   searchRange.EndLedger,
			Filters: []stellartypes.EventFilter{
				{
					EventTypes:  []stellartypes.EventType{stellartypes.EventTypeContract},
					ContractIDs: []string{fc.forwarderAddress},
					Topics:      []stellartypes.TopicFilter{topicFilter},
				},
			},
			Pagination: &stellartypes.PaginationOptions{
				Cursor: cursor,
				Limit:  reportProcessedEventPageLimit,
			},
		})
		if err != nil {
			return nil, err
		}
		if resp.LatestLedger > 0 && resp.LatestLedger < searchRange.EndLedger {
			return nil, fmt.Errorf("event index has not reached requested range: latest ledger %d, requested end ledger %d", resp.LatestLedger, searchRange.EndLedger)
		}

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

		if resp.Cursor == "" {
			return events, nil
		}
		cursor = resp.Cursor
	}

	return nil, fmt.Errorf("too many ReportProcessed event pages for range %d-%d", searchRange.StartLedger, searchRange.EndLedger)
}

func (fc *forwarderClient) GetReportProcessedEventSearchRange(ctx context.Context) (EventSearchRange, error) {
	endLedger, err := fc.GetReportProcessedEventSearchEndLedger(ctx)
	if err != nil {
		return EventSearchRange{}, err
	}
	if int64(endLedger) <= fc.forwarderLookbackLedgers {
		return EventSearchRange{StartLedger: 1, EndLedger: endLedger}, nil
	}
	start := int64(endLedger) - fc.forwarderLookbackLedgers
	return EventSearchRange{
		StartLedger: uint32(start), //nolint:gosec // G115: start is positive and at most endLedger (uint32)
		EndLedger:   endLedger,
	}, nil
}

func (fc *forwarderClient) GetReportProcessedEventSearchEndLedger(ctx context.Context) (uint32, error) {
	latest, err := fc.GetLatestLedger(ctx)
	if err != nil {
		return 0, err
	}
	return latest.Sequence, nil
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
