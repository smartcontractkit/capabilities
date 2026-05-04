package actions

import (
	"context"
	"fmt"

	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	solprimitives "github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives/solana"
	"github.com/smartcontractkit/chainlink-solana/contracts"
	lptypes "github.com/smartcontractkit/chainlink-solana/pkg/solana/logpoller/types"
)

type logReader struct {
	types.SolanaService
	forwarderProgramID solana.PublicKey
	sigProcessed       soltypes.EventSignature
	sigInProgress      soltypes.EventSignature
}

const (
	EventReportProcessed  = "ReportProcessed"
	EventReportInProgress = "ReportInProgress"
)

type LogsTransmissionStatusProvider struct {
	lr   *logReader
	lggr logger.Logger
}

func newLogTransmissionInfoProvider(ctx context.Context, lggr logger.Logger, programID solana.PublicKey, s types.SolanaService) (TransmissionInfoProvider, error) {
	lr := &logReader{
		SolanaService:      s,
		forwarderProgramID: programID,
	}

	err := lr.registerCREForwarderFilters(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to register CRE forwarder filters: %w", err)
	}

	return &LogsTransmissionStatusProvider{
		lr:   lr,
		lggr: lggr,
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
	var event ReportProcessed
	if len(successLogs) != 1 {
		return TransmissionInfo{}, fmt.Errorf("unexpected successful logs length: %d", len(successLogs))
	}

	log := successLogs[0]
	err := bin.UnmarshalBorsh(&event, log.Data)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to unmarshal report processed event: %w", err)
	}

	return TransmissionInfo{
		State:     TransmissionStateSucceeded,
		Signature: solana.Signature(log.TxHash),
	}, nil
}

func (p *LogsTransmissionStatusProvider) failedTransmissionInfoReply(inProgressLogs []*soltypes.Log) (TransmissionInfo, error) {
	var event ReportInProgress

	// use signature of the oldest transaction in reply
	log := inProgressLogs[0]
	minBlock := inProgressLogs[0].BlockNumber
	for _, l := range inProgressLogs {
		if l.BlockNumber < minBlock {
			log = l
			minBlock = l.BlockNumber
		}
	}

	err := bin.UnmarshalBorsh(&event, log.Data)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to unmarshal report in progress event: %w", err)
	}

	return TransmissionInfo{
		State:     TransmissionStateFailed,
		Signature: solana.Signature(log.TxHash),
	}, nil
}

func (lr *logReader) registerCREForwarderFilters(ctx context.Context) error {
	sigProcessed := soltypes.EventSignature(lptypes.NewEventSignatureFromName(EventReportProcessed))
	err := lr.RegisterLogTracking(ctx, soltypes.LPFilterQuery{
		Name:            EventReportProcessed + "_" + lr.forwarderProgramID.String(),
		Address:         soltypes.PublicKey(lr.forwarderProgramID),
		EventName:       EventReportProcessed,
		EventSig:        sigProcessed,
		ContractIdlJSON: []byte(contracts.FetchForwarderIDL()),
		SubkeyPaths:     [][]string{{"TransmissionId"}},
	})

	if err != nil {
		return fmt.Errorf("failed to register  EventReportProcessed filter for forwarder: %w", err)
	}

	sigInProgress := soltypes.EventSignature(lptypes.NewEventSignatureFromName(EventReportInProgress))
	err = lr.RegisterLogTracking(ctx, soltypes.LPFilterQuery{
		Name:            EventReportInProgress + "_" + lr.forwarderProgramID.String(),
		Address:         soltypes.PublicKey(lr.forwarderProgramID),
		EventName:       EventReportInProgress,
		EventSig:        sigInProgress,
		ContractIdlJSON: []byte(contracts.FetchForwarderIDL()),
		SubkeyPaths:     [][]string{{"TransmissionId"}},
		IncludeReverted: true,
	})
	if err != nil {
		return fmt.Errorf("failed to register  EventReportIntProgress filter for forwarder: %w", err)
	}

	lr.sigProcessed = sigProcessed
	lr.sigInProgress = sigInProgress

	return nil
}

func (lr *logReader) queryProcessed(ctx context.Context, transmissionID [32]byte) ([]*soltypes.Log, error) {
	limit := query.NewLimitAndSort(query.CountLimit(1), query.NewSortBySequence(query.Desc))
	queryProcessed := []query.Expression{}
	queryProcessed = append(queryProcessed, solprimitives.NewEventSigFilter(lr.sigProcessed))
	queryProcessed = append(queryProcessed, solprimitives.NewAddressFilter(soltypes.PublicKey(lr.forwarderProgramID)))

	queryProcessed = append(queryProcessed, solprimitives.NewEventBySubkeyFilter(0, []solprimitives.IndexedValueComparator{
		{Value: transmissionID[:], Operator: primitives.Eq},
	}))
	logsProcessed, err := lr.QueryTrackedLogs(ctx, queryProcessed, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query tracked logs: %w", err)
	}

	return logsProcessed, nil
}

func (lr *logReader) queryInProgress(ctx context.Context, transmissionID [32]byte) ([]*soltypes.Log, error) {
	limit := query.NewLimitAndSort(query.CountLimit(1), query.NewSortBySequence(query.Desc))
	queryInProgress := []query.Expression{}
	queryInProgress = append(queryInProgress, solprimitives.NewEventSigFilter(lr.sigInProgress))
	queryInProgress = append(queryInProgress, solprimitives.NewAddressFilter(soltypes.PublicKey(lr.forwarderProgramID)))

	queryInProgress = append(queryInProgress, solprimitives.NewEventBySubkeyFilter(0, []solprimitives.IndexedValueComparator{
		{Value: transmissionID[:], Operator: primitives.Eq},
	}))
	logsProcessed, err := lr.QueryTrackedLogs(ctx, queryInProgress, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query tracked logs: %w", err)
	}

	return logsProcessed, nil
}

type ReportProcessed struct {
	Discriminator [8]byte `bin:"-"`

	State          solana.PublicKey
	Receiver       solana.PublicKey
	TransmissionId [32]byte
	Result         bool
}

type ReportInProgress struct {
	Discriminator [8]byte `bin:"-"`

	State          solana.PublicKey
	TransmissionId [32]byte
}
