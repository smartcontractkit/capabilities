package actions

import (
	"context"
	"fmt"

	"github.com/gagliardetto/solana-go"

	"github.com/smartcontractkit/chainlink-common/pkg/types"
	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	solprimitives "github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/solana"
	"github.com/smartcontractkit/chainlink-solana/contracts"
	ks_forwarder "github.com/smartcontractkit/chainlink-solana/contracts/generated/keystone_forwarder"
	lptypes "github.com/smartcontractkit/chainlink-solana/pkg/solana/logpoller/types"
)

const (
	EventReportProcessed  = "ReportProcessed"
	EventReportInProgress = "ReportInProgress"
)

// transmissionLogSubkeyPath indexes events by `transmission_id` (same 32-byte value used with
// forwarder state to derive the on-chain execution_state / transmission PDA in the forwarder).
var transmissionLogSubkeyPath = []string{"TransmissionId"}

type logReader struct {
	types.SolanaService
	forwarderProgramID solana.PublicKey
	sigProcessed       soltypes.EventSignature
	sigInProgress      soltypes.EventSignature
}

// LogsTransmissionStatusProvider resolves transmission status from ReportProcessed (success) and
// ReportInProgress (failure, including reverted) logs via the log poller.
type LogsTransmissionStatusProvider struct {
	types.SolanaService
	forwarderProgramID solana.PublicKey
	lr                 *logReader
}

func newLogsTransmissionStatusProvider(ctx context.Context, programID solana.PublicKey, s types.SolanaService) (TransmissionInfoProvider, error) {
	lr := &logReader{
		SolanaService:      s,
		forwarderProgramID: programID,
	}
	if err := lr.registerCREForwarderFilters(ctx); err != nil {
		return nil, fmt.Errorf("failed to register forwarder log filters: %w", err)
	}
	return &LogsTransmissionStatusProvider{
		SolanaService:      s,
		forwarderProgramID: programID,
		lr:                 lr,
	}, nil
}

func (p *LogsTransmissionStatusProvider) GetTransmissionInfo(ctx context.Context, transmissionID [32]byte) (TransmissionInfo, error) {
	processedLogs, err := p.lr.queryProcessed(ctx, transmissionID)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to request processed events: %w", err)
	}

	if len(processedLogs) > 0 {
		return p.successTransmissionInfoReply(processedLogs)
	}

	inProgressLogs, err := p.lr.queryInProgress(ctx, transmissionID)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to request in progress events: %w", err)
	}

	if len(inProgressLogs) > 0 {
		return p.failedTransmissionInfoReply(inProgressLogs)
	}

	return TransmissionInfo{
		State: TransmissionStateNotAttempted,
	}, nil
}

func (p *LogsTransmissionStatusProvider) successTransmissionInfoReply(successLogs []*soltypes.Log) (TransmissionInfo, error) {
	if len(successLogs) != 1 {
		return TransmissionInfo{}, fmt.Errorf("unexpected successful logs length: %d", len(successLogs))
	}

	log := successLogs[0]
	_, err := ks_forwarder.ParseEvent_ReportProcessed(log.Data)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to unmarshal report processed event: %w", err)
	}

	return TransmissionInfo{
		State:     TransmissionStateSucceeded,
		Signature: solana.Signature(log.TxHash),
	}, nil
}

func (p *LogsTransmissionStatusProvider) failedTransmissionInfoReply(inProgressLogs []*soltypes.Log) (TransmissionInfo, error) {
	// use signature of the oldest transaction in reply
	log := inProgressLogs[0]
	minBlock := inProgressLogs[0].BlockNumber
	for _, l := range inProgressLogs {
		if l.BlockNumber < minBlock {
			log = l
			minBlock = l.BlockNumber
		}
	}

	_, err := ks_forwarder.ParseEvent_ReportInProgress(log.Data)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to unmarshal report in progress event: %w", err)
	}

	return TransmissionInfo{
		State:     TransmissionStateFailed,
		Signature: solana.Signature(log.TxHash),
	}, nil
}

func (lr *logReader) registerCREForwarderFilters(ctx context.Context) error {
	idlJSON := []byte(contracts.FetchForwarderIDL())

	sigProcessed := soltypes.EventSignature(lptypes.NewEventSignatureFromName(EventReportProcessed))
	err := lr.RegisterLogTracking(ctx, soltypes.LPFilterQuery{
		Name:            EventReportProcessed + "_" + lr.forwarderProgramID.String(),
		Address:         soltypes.PublicKey(lr.forwarderProgramID),
		EventName:       EventReportProcessed,
		EventSig:        sigProcessed,
		ContractIdlJSON: idlJSON,
		SubkeyPaths:     [][]string{transmissionLogSubkeyPath},
	})
	if err != nil {
		return fmt.Errorf("failed to register ReportProcessed filter for forwarder: %w", err)
	}

	sigInProgress := soltypes.EventSignature(lptypes.NewEventSignatureFromName(EventReportInProgress))
	err = lr.RegisterLogTracking(ctx, soltypes.LPFilterQuery{
		Name:            EventReportInProgress + "_" + lr.forwarderProgramID.String(),
		Address:         soltypes.PublicKey(lr.forwarderProgramID),
		EventName:       EventReportInProgress,
		EventSig:        sigInProgress,
		ContractIdlJSON: idlJSON,
		SubkeyPaths:     [][]string{transmissionLogSubkeyPath},
		IncludeReverted: true,
	})
	if err != nil {
		return fmt.Errorf("failed to register ReportInProgress filter for forwarder: %w", err)
	}

	lr.sigProcessed = sigProcessed
	lr.sigInProgress = sigInProgress
	return nil
}

func (lr *logReader) queryProcessed(ctx context.Context, transmissionID [32]byte) ([]*soltypes.Log, error) {
	limit := query.NewLimitAndSort(query.CountLimit(1), query.NewSortBySequence(query.Desc))
	exprs := []query.Expression{
		solprimitives.NewEventSigFilter(lr.sigProcessed),
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
