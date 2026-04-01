package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

// OnChainTransmissionInfoProvider uses the ExecutionState account for transmission success/failure
// (avoids relying on ReportProcessed logs, which can be truncated). Tx signatures for replies and
// fee calculation still come from ReportInProgress events via the log poller.
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
	execStateAddr, err := deriveExecutionStatePDA(p.forwarderState, transmissionID, p.forwarderProgramID)
	if err != nil {
		return nil, fmt.Errorf("failed to derive execution state PDA: %w", err)
	}

	reply, err := p.SolanaService.GetAccountInfoWithOpts(ctx, soltypes.GetAccountInfoRequest{
		Account: soltypes.PublicKey(execStateAddr),
		Opts: &soltypes.GetAccountInfoOpts{
			Commitment: soltypes.CommitmentProcessed,
		},
	})
	if err != nil {
		if errors.Is(err, rpc.ErrNotFound) {
			reply = &soltypes.GetAccountInfoReply{}
		} else {
			return nil, fmt.Errorf("failed to get execution state account: %w", err)
		}
	}

	inProgressLogs, err := p.lr.queryInProgress(ctx, transmissionID)
	if err != nil {
		return nil, fmt.Errorf("failed to request ReportInProgress events: %w", err)
	}

	if reply.Value == nil {
		if len(inProgressLogs) > 0 {
			sig, sigErr := signatureFromInProgressLogs(inProgressLogs)
			if sigErr != nil {
				return nil, sigErr
			}
			return &TransmissionInfo{State: TransmissionStateFailed, Signature: sig}, nil
		}
		return &TransmissionInfo{State: TransmissionStateNotAttempted}, nil
	}

	raw, ok := accountDataBytes(reply.Value)
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("execution state account has no binary data")
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

	if len(inProgressLogs) == 0 {
		return nil, fmt.Errorf("execution state on-chain but no ReportInProgress log for transmission")
	}

	sig, err := signatureFromInProgressLogs(inProgressLogs)
	if err != nil {
		return nil, err
	}

	return &TransmissionInfo{
		State:     state,
		Signature: sig,
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

func accountDataBytes(acc *soltypes.Account) ([]byte, bool) {
	if acc == nil || acc.Data == nil {
		return nil, false
	}
	if len(acc.Data.AsDecodedBinary) > 0 {
		return acc.Data.AsDecodedBinary, true
	}
	return nil, false
}
