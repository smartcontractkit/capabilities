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
	lptypes "github.com/smartcontractkit/chainlink-solana/pkg/solana/logpoller/types"
)

const eventReportInProgress = "ReportInProgress"
const eventReportProcessed = "ReportProcessed"

// transmissionLogSubkeyPath indexes forwarder events by transmission_id.
var transmissionLogSubkeyPath = []string{"TransmissionId"}

// stateSubkeyPath indexes forwarder events by forwarder_state.
var stateSubkeyPath = []string{"State"}

type logReader struct {
	types.SolanaService
	forwarderProgramID solana.PublicKey
	forwarderState     solana.PublicKey
	sigInProgress      soltypes.EventSignature
	sigProcessed       soltypes.EventSignature
}

// OnChainTransmissionInfoProvider derives transmission state from tracked forwarder logs.
// A ReportProcessed log is tracked only from successfully executed transactions, so its
// presence proves success and its tx hash is always a successful tx signature.
// ReportInProgress logs are tracked including reverted transactions: they prove an attempt
// was made and, absent a ReportProcessed log, that every attempt so far failed.
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
		forwarderState:     forwarderState,
	}
	if err := lr.registerInProgressFilter(ctx); err != nil {
		return nil, fmt.Errorf("failed to register ReportInProgress log filter: %w", err)
	}
	if err := lr.unregisterLegacyInProgressFilter(ctx); err != nil {
		return nil, fmt.Errorf("failed to unregister legacy ReportInProgress log filter: %w", err)
	}
	if err := lr.registerProcessedFilter(ctx); err != nil {
		return nil, fmt.Errorf("failed to register ReportProcessed log filter: %w", err)
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

	// The ReportProcessed filter excludes reverted transactions, so any tracked
	// ReportProcessed log comes from a successfully executed tx.
	processedLogs, err := p.lr.queryProcessed(ctx, transmissionID)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to request ReportProcessed events: %w", err)
	}
	if len(processedLogs) > 0 {
		return TransmissionInfo{
			State:     TransmissionStateSucceeded,
			Signature: solana.Signature(processedLogs[0].TxHash),
		}, nil
	}

	// ReportInProgress without ReportProcessed: the report was attempted, but every
	// attempt so far landed in a reverted tx (a successful tx would have emitted a
	// tracked ReportProcessed log).
	return TransmissionInfo{State: TransmissionStateFailed, Signature: solana.Signature(inProgressLogs[0].TxHash)}, nil
}

// Legacy LogTracking doesn't validate against forwarder state used in Event.
// Unregistering non-existing filter is no-op
func (lr *logReader) unregisterLegacyInProgressFilter(ctx context.Context) error {
	return lr.UnregisterLogTracking(ctx, eventReportInProgress+"_"+lr.forwarderProgramID.String())
}

func (lr *logReader) registerInProgressFilter(ctx context.Context) error {
	idlJSON := []byte(contracts.FetchForwarderIDL())
	sigInProgress := soltypes.EventSignature(lptypes.NewEventSignatureFromName(eventReportInProgress))
	err := lr.RegisterLogTracking(ctx, soltypes.LPFilterQuery{
		Name:            eventReportInProgress + "_" + lr.forwarderProgramID.String() + "_v2",
		Address:         soltypes.PublicKey(lr.forwarderProgramID),
		EventName:       eventReportInProgress,
		EventSig:        sigInProgress,
		ContractIdlJSON: idlJSON,
		SubkeyPaths:     [][]string{transmissionLogSubkeyPath, stateSubkeyPath},
		IncludeReverted: true,
	})
	if err != nil {
		return fmt.Errorf("failed to register ReportInProgress filter for forwarder: %w", err)
	}

	lr.sigInProgress = sigInProgress
	return nil
}

func (lr *logReader) registerProcessedFilter(ctx context.Context) error {
	idlJSON := []byte(contracts.FetchForwarderIDL())
	sigProcessed := soltypes.EventSignature(lptypes.NewEventSignatureFromName(eventReportProcessed))
	err := lr.RegisterLogTracking(ctx, soltypes.LPFilterQuery{
		Name:            eventReportProcessed + "_" + lr.forwarderProgramID.String() + "_v2",
		Address:         soltypes.PublicKey(lr.forwarderProgramID),
		EventName:       eventReportProcessed,
		EventSig:        sigProcessed,
		ContractIdlJSON: idlJSON,
		SubkeyPaths:     [][]string{transmissionLogSubkeyPath, stateSubkeyPath},
		// Only successfully executed transactions are tracked, so signatures queried
		// through this filter always belong to a successful tx.
		IncludeReverted: false,
	})
	if err != nil {
		return fmt.Errorf("failed to register ReportProcessed filter for forwarder: %w", err)
	}

	lr.sigProcessed = sigProcessed
	return nil
}

const inProgressLogsLimit = 1

func (lr *logReader) queryInProgress(ctx context.Context, transmissionID [32]byte) ([]*soltypes.Log, error) {
	limit := query.NewLimitAndSort(query.CountLimit(inProgressLogsLimit), query.NewSortBySequence(query.Asc))
	exprs := []query.Expression{
		solprimitives.NewEventSigFilter(lr.sigInProgress),
		solprimitives.NewAddressFilter(soltypes.PublicKey(lr.forwarderProgramID)),
		solprimitives.NewEventBySubkeyFilter(0, []solprimitives.IndexedValueComparator{
			{Value: transmissionID[:], Operator: primitives.Eq},
		}),
		solprimitives.NewEventBySubkeyFilter(1, []solprimitives.IndexedValueComparator{
			{Value: lr.forwarderState.Bytes(), Operator: primitives.Eq},
		}),
	}

	logs, err := lr.QueryTrackedLogs(ctx, exprs, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query tracked logs: %w", err)
	}

	return logs, nil
}

func (lr *logReader) queryProcessed(ctx context.Context, transmissionID [32]byte) ([]*soltypes.Log, error) {
	limit := query.NewLimitAndSort(query.CountLimit(1), query.NewSortBySequence(query.Asc))
	exprs := []query.Expression{
		solprimitives.NewEventSigFilter(lr.sigProcessed),
		solprimitives.NewAddressFilter(soltypes.PublicKey(lr.forwarderProgramID)),
		solprimitives.NewEventBySubkeyFilter(0, []solprimitives.IndexedValueComparator{
			{Value: transmissionID[:], Operator: primitives.Eq},
		}),
		solprimitives.NewEventBySubkeyFilter(1, []solprimitives.IndexedValueComparator{
			{Value: lr.forwarderState.Bytes(), Operator: primitives.Eq},
		}),
	}

	logs, err := lr.QueryTrackedLogs(ctx, exprs, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query tracked logs: %w", err)
	}
	return logs, nil
}
