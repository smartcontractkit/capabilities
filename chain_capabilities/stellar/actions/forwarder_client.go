package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

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

	args, err := buildForwarderReportArgs(transmitter, receiver, report)
	if err != nil {
		return nil, err
	}

	submitResp, err := fc.SubmitTransaction(ctx, stellartypes.SubmitTransactionRequest{
		IdempotencyKey:     uuid.NewString(),
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
	args, err := buildTransmissionInfoArgs(receiver, workflowExecutionID, reportID)
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
		if strings.Contains(strings.ToLower(resp.Error), "missing") || strings.Contains(strings.ToLower(resp.Error), "not found") {
			return TransmissionInfo{State: TransmissionStateNotAttempted}, nil
		}
		return TransmissionInfo{}, fmt.Errorf("forwarder simulation failed: %s", resp.Error)
	}
	if resp.ReturnValueXDR == "" {
		return TransmissionInfo{State: TransmissionStateNotAttempted}, nil
	}

	return parseTransmissionInfo(resp.ReturnValueXDR, resp.LedgerSequence)
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
	topicFilter, err := buildReportProcessedTopicFilter(receiver, workflowExecutionID, reportID)
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

func buildTransmissionInfoArgs(receiver string, workflowExecutionID [32]byte, reportID [2]byte) ([]stellartypes.ScVal, error) {
	receiverVal, err := contractAddressToScVal(receiver)
	if err != nil {
		return nil, err
	}
	return []stellartypes.ScVal{
		receiverVal,
		{Type: stellartypes.ScValTypeBytes, Bytes: workflowExecutionID[:]},
		{Type: stellartypes.ScValTypeBytes, Bytes: reportID[:]},
	}, nil
}

func buildReportProcessedTopicFilter(receiver string, workflowExecutionID [32]byte, reportID [2]byte) (stellartypes.TopicFilter, error) {
	eventName := reportProcessedTopicPrefix
	receiverVal, err := contractAddressToScVal(receiver)
	if err != nil {
		return stellartypes.TopicFilter{}, err
	}
	return stellartypes.TopicFilter{
		Segments: []stellartypes.TopicSegment{
			{Value: &stellartypes.ScVal{Type: stellartypes.ScValTypeSymbol, Symbol: &eventName}},
			{Value: &receiverVal},
			{Value: &stellartypes.ScVal{Type: stellartypes.ScValTypeBytes, Bytes: workflowExecutionID[:]}},
			{Value: &stellartypes.ScVal{Type: stellartypes.ScValTypeBytes, Bytes: reportID[:]}},
		},
	}, nil
}

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
