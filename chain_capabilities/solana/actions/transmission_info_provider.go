package actions

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/smartcontractkit/chainlink-common/pkg/types"
	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	solprimitives "github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/solana"
	"github.com/smartcontractkit/chainlink-solana/contracts"
	ks_forwarder "github.com/smartcontractkit/chainlink-solana/contracts/generated/keystone_forwarder"
	lptypes "github.com/smartcontractkit/chainlink-solana/pkg/solana/logpoller/types"
)

const eventReportInProgress = "ReportInProgress"

// transmissionLogSubkeyPath indexes ReportInProgress by transmission_id.
var transmissionLogSubkeyPath = []string{"TransmissionId"}

type logReader struct {
	types.SolanaService
	forwarderProgramID solana.PublicKey
	sigInProgress      soltypes.EventSignature
}

// OnChainTransmissionInfoProvider uses the ExecutionState PDA for success/failure and
// ReportInProgress logs for transaction signatures (ReportProcessed is not used; it can be truncated).
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

func (p *OnChainTransmissionInfoProvider) GetTransmissionInfo(ctx context.Context, transmissionID [32]byte) (TransmissionInfo, error) {
	inProgressLogs, err := p.lr.queryInProgress(ctx, transmissionID)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to request ReportInProgress events: %w", err)
	}

	if len(inProgressLogs) == 0 {
		return TransmissionInfo{State: TransmissionStateNotAttempted}, nil
	}

	execStateAddr, err := deriveExecutionStatePDA(p.forwarderState, transmissionID, p.forwarderProgramID)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to derive execution state PDA: %w", err)
	}

	reply, err := p.SolanaService.GetAccountInfoWithOpts(ctx, soltypes.GetAccountInfoRequest{
		Account: soltypes.PublicKey(execStateAddr),
		Opts: &soltypes.GetAccountInfoOpts{
			Commitment: soltypes.CommitmentProcessed,
		},
	})
	if err != nil {
		if isExecutionStateAccountMissing(err) {
			reply = &soltypes.GetAccountInfoReply{}
		} else {
			return TransmissionInfo{}, fmt.Errorf("failed to get execution state account: %w", err)
		}
	}

	sig, sigErr := signatureFromInProgressLogs(inProgressLogs)
	if sigErr != nil {
		return TransmissionInfo{}, sigErr
	}

	raw, haveBinary := accountDataBytesForTransmission(reply)
	if !haveBinary {
		// ReportInProgress but no decodable account payload (reverted tx, or missing data on wire).
		return TransmissionInfo{State: TransmissionStateFailed, Signature: sig}, nil
	}

	execState, err := ks_forwarder.ParseAccount_ExecutionState(raw)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to parse execution state account: %w", err)
	}

	if !bytes.Equal(execState.TransmissionId[:], transmissionID[:]) {
		return TransmissionInfo{}, fmt.Errorf("execution state transmission id mismatch")
	}

	var state TransmissionState
	if execState.Success {
		state = TransmissionStateSucceeded
	} else {
		state = TransmissionStateFailed
	}

	return TransmissionInfo{
		State:     state,
		Signature: sig,
	}, nil
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

func signatureFromInProgressLogs(inProgressLogs []*soltypes.Log) (solana.Signature, error) {
	if len(inProgressLogs) == 0 {
		return solana.Signature{}, fmt.Errorf("no in-progress logs")
	}
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
	idlJSON := []byte(contracts.FetchForwarderIDL())
	sigInProgress := soltypes.EventSignature(lptypes.NewEventSignatureFromName(eventReportInProgress))
	err := lr.RegisterLogTracking(ctx, soltypes.LPFilterQuery{
		Name:            eventReportInProgress + "_" + lr.forwarderProgramID.String(),
		Address:         soltypes.PublicKey(lr.forwarderProgramID),
		EventName:       eventReportInProgress,
		EventSig:        sigInProgress,
		ContractIdlJSON: idlJSON,
		SubkeyPaths:     [][]string{transmissionLogSubkeyPath},
		IncludeReverted: true,
	})
	if err != nil {
		return fmt.Errorf("failed to register ReportInProgress filter for forwarder: %w", err)
	}

	lr.sigInProgress = sigInProgress
	return nil
}

func (lr *logReader) queryInProgress(ctx context.Context, transmissionID [32]byte) ([]*soltypes.Log, error) {
	limit := query.NewLimitAndSort(query.CountLimit(1), query.NewSortBySequence(query.Desc))
	exprs := []query.Expression{
		solprimitives.NewEventSigFilter(lr.sigInProgress),
		solprimitives.NewAddressFilter(soltypes.PublicKey(lr.forwarderProgramID)),
		solprimitives.NewEventBySubkeyFilter(0, []solprimitives.IndexedValueComparator{
			{Value: transmissionID[:], Operator: primitives.Eq},
		}),
	}

	logs, err := lr.QueryTrackedLogs(ctx, exprs, limit)
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
