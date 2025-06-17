package oracle

import (
	"context"
	"encoding/hex"
	"fmt"

	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/smartcontractkit/libocr/quorumhelper"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

type blocksProvider interface {
	GetLatest() (*pb.BigInt, error)
	GetSafe() (*pb.BigInt, error)
	GetFinalized() (*pb.BigInt, error)
}

var _ ocr3types.ReportingPlugin[[]byte] = (*reportingPlugin)(nil)

type reportingPlugin struct {
	config         ocr3types.ReportingPluginConfig
	logger         logger.SugaredLogger
	blocksProvider blocksProvider
}

func NewReportingPlugin(
	config ocr3types.ReportingPluginConfig,
	logger logger.SugaredLogger,
	blocksProvider blocksProvider,
) ocr3types.ReportingPlugin[[]byte] {
	return &reportingPlugin{
		config:         config,
		logger:         logger,
		blocksProvider: blocksProvider,
	}
}

func (rp *reportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	return nil, nil
}

func (rp *reportingPlugin) Observation(
	ctx context.Context,
	outctx ocr3types.OutcomeContext,
	query types.Query,
) (types.Observation, error) {
	observation := &evmservice.Observations{}
	var err error
	observation.Finalized, err = rp.blocksProvider.GetFinalized()
	if err != nil {
		return nil, fmt.Errorf("failed to get finalized block height: %w", err)
	}

	observation.Safe, err = rp.blocksProvider.GetSafe()
	if err != nil {
		return nil, fmt.Errorf("failed to get safe block height: %w", err)
	}

	observation.Latest, err = rp.blocksProvider.GetLatest()
	if err != nil {
		return nil, fmt.Errorf("failed to get latest block height: %w", err)
	}

	err = validateBlockHeight(observation)
	if err != nil {
		return nil, fmt.Errorf("invalid block height: %w", err)
	}

	if len(outctx.PreviousOutcome) > 0 {
		var previousOutcome evmservice.Outcome
		err := proto.Unmarshal(outctx.PreviousOutcome, &previousOutcome)
		if err != nil {
			rp.logger.Errorw("Could not unmarshal previous outcome", "err", err, "previousOutcome", hex.EncodeToString(outctx.PreviousOutcome))
			return nil, fmt.Errorf("could not unmarshal previous outcome: %w", err)
		}

		observation.Finalized = maxProtoBigInt(observation.Finalized, previousOutcome.Finalized)
		observation.Safe = maxProtoBigInt(observation.Safe, previousOutcome.Safe, observation.Finalized)
		observation.Latest = maxProtoBigInt(observation.Latest, previousOutcome.Latest, observation.Safe)
	}

	rp.logger.Debugw("Observation complete",
		"observation", observation,
	)

	return proto.Marshal(observation)
}

func (rp *reportingPlugin) ValidateObservation(_ context.Context, outctx ocr3types.OutcomeContext, query types.Query, ao types.AttributedObservation) error {
	ob := new(evmservice.Observations)
	if err := proto.Unmarshal(ao.Observation, ob); err != nil {
		return fmt.Errorf("could not unmarshal proposed outcome: %w", err)
	}

	err := validateBlockHeight(ob)
	if err != nil {
		return fmt.Errorf("invalid block height: %w", err)
	}

	if len(outctx.PreviousOutcome) > 0 {
		prev := new(evmservice.Outcome)
		err := proto.Unmarshal(outctx.PreviousOutcome, prev)
		if err != nil {
			rp.logger.Errorw("Could not unmarshal previous outcome", "err", err, "previousOutcome", hex.EncodeToString(outctx.PreviousOutcome))
			return fmt.Errorf("could not unmarshal previous outcome: %w", err)
		}

		err = validateBlockHeightAgainstOutcome(ob, prev)
		if err != nil {
			return fmt.Errorf("invalid block height: %w", err)
		}
	}

	return nil
}

func (rp *reportingPlugin) ObservationQuorum(_ context.Context, outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (quorumReached bool, err error) {
	return quorumhelper.ObservationCountReachesObservationQuorum(quorumhelper.QuorumByzQuorum, rp.config.N, rp.config.F, aos), nil
}

func (rp *reportingPlugin) Outcome(
	_ context.Context,
	outctx ocr3types.OutcomeContext,
	query types.Query,
	aos []types.AttributedObservation,
) (ocr3types.Outcome, error) {
	observations := make([]evmservice.Observations, len(aos))
	for i, ao := range aos {
		if err := proto.Unmarshal(ao.Observation, &observations[i]); err != nil {
			return nil, fmt.Errorf("could not unmarshal proposed outcome: %w", err)
		}
	}

	var outcome evmservice.Outcome
	var err error
	outcome.Latest, err = fPlusOneLowestBlockHeight(observations, rp.config.F, func(o *evmservice.Observations) *pb.BigInt { return o.Latest })
	if err != nil {
		return nil, fmt.Errorf("could not determine latest block height: %w", err)
	}

	outcome.Safe, err = fPlusOneLowestBlockHeight(observations, rp.config.F, func(o *evmservice.Observations) *pb.BigInt { return o.Safe })
	if err != nil {
		return nil, fmt.Errorf("could not determine safe block height: %w", err)
	}

	outcome.Finalized, err = fPlusOneLowestBlockHeight(observations, rp.config.F, func(o *evmservice.Observations) *pb.BigInt { return o.Finalized })
	if err != nil {
		return nil, fmt.Errorf("could not determine finalized block height: %w", err)
	}

	return proto.Marshal(&outcome)
}

func (rp *reportingPlugin) Reports(ctx context.Context, seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	return nil, nil
}

func (rp *reportingPlugin) ShouldAcceptAttestedReport(
	ctx context.Context,
	seqNr uint64,
	reportWithInfo ocr3types.ReportWithInfo[[]byte],
) (bool, error) {
	return true, nil
}

func (rp *reportingPlugin) ShouldTransmitAcceptedReport(
	ctx context.Context,
	seqNr uint64,
	reportWithInfo ocr3types.ReportWithInfo[[]byte],
) (bool, error) {
	return true, nil
}

func (rp *reportingPlugin) Close() error {
	return nil
}
