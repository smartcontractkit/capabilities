package actions

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	solprimitives "github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/solana"
	"github.com/smartcontractkit/chainlink-solana/contracts"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/codec"
	lptypes "github.com/smartcontractkit/chainlink-solana/pkg/solana/logpoller/types"
)

const eventReportInProgress = "ReportInProgress"

// logReader holds ReportInProgress log poller state for resolving transaction signatures.
type logReader struct {
	types.SolanaService
	forwarderProgramID solana.PublicKey
	sigInProgress      soltypes.EventSignature
}

// OnChainTransmissionInfoProvider uses the ExecutionState account for success/failure when
// ReportInProgress is present (avoids relying on truncated ReportProcessed logs). If there is no
// ReportInProgress log, it returns NotAttempted without an RPC read. Tx signatures come from
// ReportInProgress via the log poller.
type OnChainTransmissionInfoProvider struct {
	types.SolanaService
	forwarderProgramID solana.PublicKey
	forwarderState     solana.PublicKey
	lr                 *logReader
}

func newOnChainTransmissionInfoProvider(ctx context.Context, programID, forwarderState solana.PublicKey, s types.SolanaService) (TransmissionInfoProvider, error) {
	lr := &logReader{
		SolanaService:      s,
		forwarderProgramID: programID,
	}
	if err := lr.registerInProgressFilter(ctx); err != nil {
		return nil, fmt.Errorf("failed to register ReportInProgress log filter: %w", err)
	}
	return &OnChainTransmissionInfoProvider{
		SolanaService:      s,
		forwarderProgramID: programID,
		forwarderState:     forwarderState,
		lr:                 lr,
	}, nil
}

func (p *OnChainTransmissionInfoProvider) GetTransmissionInfo(ctx context.Context, transmissionID [32]byte) (*TransmissionInfo, error) {
	inProgressLogs, err := p.lr.queryInProgress(ctx, transmissionID)
	if err != nil {
		return nil, fmt.Errorf("failed to request ReportInProgress events: %w", err)
	}
	if len(inProgressLogs) == 0 {
		// No attempt observed in logs; skip execution-state PDA fetch.
		return &TransmissionInfo{State: TransmissionStateNotAttempted}, nil
	}

	sigFromLog, err := signatureFromInProgressLogs(inProgressLogs)
	if err != nil {
		return nil, err
	}

	execStateAddr, err := deriveExecutionStatePDA(p.forwarderState, transmissionID, p.forwarderProgramID)
	if err != nil {
		return nil, fmt.Errorf("failed to derive execution state PDA: %w", err)
	}

	reply, err := p.SolanaService.GetAccountInfoWithOpts(ctx, soltypes.GetAccountInfoRequest{
		Account: soltypes.PublicKey(execStateAddr),
		Opts: &soltypes.GetAccountInfoOpts{
			Commitment: soltypes.CommitmentConfirmed,
			// jsonParsed: RPC returns account data as JSON; unparsed programs fall back to
			// ["<base64>","base64"]. accountDataBytesForTransmission decodes AsDecodedBinary or AsJSON.
			Encoding: soltypes.EncodingJSONParsed,
		},
	})
	if err != nil {
		if isExecutionStateAccountMissing(err) {
			reply = &soltypes.GetAccountInfoReply{}
		} else {
			return nil, fmt.Errorf("failed to get execution state account: %w", err)
		}
	}

	raw, haveBinary := accountDataBytesForTransmission(reply)
	if !haveBinary {
		// ReportInProgress but no decodable account payload (reverted tx, or missing data on wire).
		return &TransmissionInfo{State: TransmissionStateFailed, Signature: sigFromLog}, nil
	}

	_, parsedTransmissionID, execSuccess, err := parseExecutionStateAccount(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse execution state account: %w", err)
	}

	if !bytes.Equal(parsedTransmissionID[:], transmissionID[:]) {
		return nil, fmt.Errorf("execution state transmission id mismatch")
	}

	var state TransmissionState
	if execSuccess {
		state = TransmissionStateSucceeded
	} else {
		state = TransmissionStateFailed
	}

	return &TransmissionInfo{
		State:     state,
		Signature: sigFromLog,
	}, nil
}

func signatureFromInProgressLogs(inProgressLogs []*soltypes.Log) (solana.Signature, error) {
	if len(inProgressLogs) == 0 {
		return solana.Signature{}, fmt.Errorf("no in-progress logs")
	}
	// Use signature of the oldest transaction in reply (same as legacy log-based provider).
	log := inProgressLogs[0]
	minBlock := inProgressLogs[0].BlockNumber
	for _, l := range inProgressLogs {
		if l.BlockNumber < minBlock {
			log = l
			minBlock = l.BlockNumber
		}
	}
	return solana.Signature(log.TxHash), nil
}

func (lr *logReader) registerInProgressFilter(ctx context.Context) error {
	var codecIDL codec.IDL
	if err := json.Unmarshal([]byte(contracts.FetchForwarderIDL()), &codecIDL); err != nil {
		return fmt.Errorf("unexpected error: invalid Forwarder IDL, error: %w", err)
	}

	eventIDLInProgress, err := getEventIDL(eventReportInProgress, codecIDL)
	if err != nil {
		return err
	}
	sigInProgress := soltypes.EventSignature(lptypes.NewEventSignatureFromName(eventReportInProgress))
	err = lr.SolanaService.RegisterLogTracking(ctx, soltypes.LPFilterQuery{
		Name:            eventReportInProgress + "_" + lr.forwarderProgramID.String(),
		Address:         soltypes.PublicKey(lr.forwarderProgramID),
		EventName:       eventReportInProgress,
		EventSig:        sigInProgress,
		ContractIdlJSON: eventIDLInProgress,
		SubkeyPaths:     [][]string{{"TransmissionId"}},
		IncludeReverted: true,
	})
	if err != nil {
		return fmt.Errorf("failed to register ReportInProgress filter for forwarder: %w", err)
	}

	lr.sigInProgress = sigInProgress
	return nil
}

func getEventIDL(eventName string, codecIDL codec.IDL) ([]byte, error) {
	eventIdl, err := extractEventIDL(eventName, codecIDL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract event IDL %s: %w", eventName, err)
	}

	lpEventIdl := lptypes.EventIdl{Event: eventIdl, Types: codecIDL.Types}

	ret, err := json.Marshal(lpEventIdl)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event IDL %s: %w", eventName, err)
	}

	return ret, nil
}

func extractEventIDL(eventName string, codecIDL codec.IDL) (codec.IdlEvent, error) {
	idlDef, err := codec.FindDefinitionFromIDL(codec.ChainConfigTypeEventDef, eventName, codecIDL)
	if err != nil {
		return codec.IdlEvent{}, err
	}
	eventIdl, isOk := idlDef.(codec.IdlEvent)
	if !isOk {
		return codec.IdlEvent{}, fmt.Errorf("unexpected type from IDL definition for event read: %q", eventName)
	}
	return eventIdl, nil
}

func (lr *logReader) queryInProgress(ctx context.Context, transmissionID [32]byte) ([]*soltypes.Log, error) {
	limit := query.NewLimitAndSort(query.CountLimit(1), query.NewSortBySequence(query.Desc))
	queryInProgress := []query.Expression{}
	queryInProgress = append(queryInProgress, solprimitives.NewEventSigFilter(lr.sigInProgress))
	queryInProgress = append(queryInProgress, solprimitives.NewAddressFilter(soltypes.PublicKey(lr.forwarderProgramID)))

	queryInProgress = append(queryInProgress, solprimitives.NewEventBySubkeyFilter(0, []solprimitives.IndexedValueComparator{
		{Value: transmissionID[:], Operator: primitives.Eq},
	}))
	logs, err := lr.SolanaService.QueryTrackedLogs(ctx, queryInProgress, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query tracked logs: %w", err)
	}

	return logs, nil
}

func deriveExecutionStatePDA(forwarderState solana.PublicKey, transmissionID [32]byte, programID solana.PublicKey) (solana.PublicKey, error) {
	seeds := [][]byte{
		[]byte("execution_state"),
		forwarderState.Bytes(),
		transmissionID[:],
	}
	ret, _, err := solana.FindProgramAddress(seeds, programID)
	return ret, err
}

// Anchor account discriminator for keystone_forwarder::state::ExecutionState (first 8 bytes of
// sha256("account:ExecutionState")). Must match the deployed forwarder program.
var executionStateAccountDiscriminator = [8]byte{31, 209, 35, 133, 132, 142, 151, 100}

// executionStateAccount is the Borsh payload after the Anchor 8-byte discriminator (matches on-chain
// keystone_forwarder::state::ExecutionState).
type executionStateAccount struct {
	Transmitter    solana.PublicKey
	TransmissionID [32]byte
	Success        bool
}

func parseExecutionStateAccount(data []byte) (transmitter solana.PublicKey, transmissionID [32]byte, success bool, err error) {
	const discLen = 8
	if len(data) < discLen+32+32+1 {
		return solana.PublicKey{}, [32]byte{}, false, fmt.Errorf("execution state account data too short: %d", len(data))
	}
	if !bytes.Equal(data[:discLen], executionStateAccountDiscriminator[:]) {
		return solana.PublicKey{}, [32]byte{}, false, fmt.Errorf("unexpected ExecutionState account discriminator")
	}

	var acc executionStateAccount
	if err := bin.UnmarshalBorsh(&acc, data[discLen:]); err != nil {
		return solana.PublicKey{}, [32]byte{}, false, fmt.Errorf("unmarshal execution state: %w", err)
	}
	return acc.Transmitter, acc.TransmissionID, acc.Success, nil
}

func accountDataBytesFromJSON(asJSON []byte) ([]byte, error) {
	if len(asJSON) == 0 {
		return nil, fmt.Errorf("empty account data json")
	}
	var arr []string
	if err := json.Unmarshal(asJSON, &arr); err == nil && len(arr) >= 2 && arr[1] == "base64" {
		return base64.StdEncoding.DecodeString(arr[0])
	}
	var wrapped struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(asJSON, &wrapped); err == nil && len(wrapped.Data) > 0 {
		return accountDataBytesFromJSON(wrapped.Data)
	}
	return nil, fmt.Errorf("could not extract base64 account data from json")
}

func isExecutionStateAccountMissing(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, rpc.ErrNotFound) {
		return true
	}
	s := strings.ToLower(err.Error())
	if !strings.Contains(s, "not found") {
		return false
	}
	return strings.Contains(s, "account info") || strings.Contains(s, "getaccountinfo")
}

// accountDataBytesForTransmission returns raw program data for Anchor parsing. Prefers
// AsDecodedBinary; if empty (e.g. jsonParsed-only path), decodes Solana's ["base64","base64"] from AsJSON.
func accountDataBytesForTransmission(reply *soltypes.GetAccountInfoReply) ([]byte, bool) {
	if reply == nil || reply.Value == nil || reply.Value.Data == nil {
		return nil, false
	}
	d := reply.Value.Data
	if len(d.AsDecodedBinary) > 0 {
		return d.AsDecodedBinary, true
	}
	raw, err := accountDataBytesFromJSON(d.AsJSON)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	return raw, true
}
