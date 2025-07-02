package oracle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/smartcontractkit/libocr/quorumhelper"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	ctypes "github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
)

var _ ocr3types.ReportingPlugin[[]byte] = (*reportingPlugin)(nil)

type Config struct {
	ocr3types.ReportingPluginConfig
	BatchSize int // max number of requests that this node will try to process in a single round
	// MaxAllowedBatchSize - defines max number of requests that this node will process in a round, if requested by another node.
	// Needed to allow graceful roll out of BatchSize increase.
	MaxAllowedBatchSize int
}

type reportingPlugin struct {
	config         Config
	logger         logger.SugaredLogger
	blocksProvider BlocksProvider
	requestsStore  RequestsHandler
}

func newReportingPlugin(
	config Config,
	logger logger.SugaredLogger,
	blocksProvider BlocksProvider,
	requestsStore RequestsHandler,
) *reportingPlugin {
	return &reportingPlugin{
		config:         config,
		logger:         logger,
		blocksProvider: blocksProvider,
		requestsStore:  requestsStore,
	}
}

func (rp *reportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	ids, err := rp.requestsStore.GetRequestIDs(rp.config.BatchSize)
	if err != nil {
		return types.Query{}, fmt.Errorf("failed to get request ready for processing IDs: %w", err)
	}

	rp.logger.Debugw("Query complete", "ids", ids)
	return proto.Marshal(&ctypes.Query{RequestIDs: ids})
}

func (rp *reportingPlugin) populateHeightFromPreviousOutcome(
	observation *ctypes.Observation,
	outctx ocr3types.OutcomeContext,
) {
	if len(outctx.PreviousOutcome) == 0 {
		return
	}

	var previousOutcome ctypes.Outcome
	err := proto.Unmarshal(outctx.PreviousOutcome, &previousOutcome)
	if err != nil {
		rp.logger.Errorw("Could not unmarshal previous outcome", "err", err, "previousOutcome", hex.EncodeToString(outctx.PreviousOutcome))
		return
	}

	// TODO PLEX-1572: report observed chain height
	prevChainHeight := previousOutcome.ChainHeight
	observation.ChainHeight.Finalized = max(observation.ChainHeight.Finalized, prevChainHeight.Finalized)
	observation.ChainHeight.Safe = max(observation.ChainHeight.Safe, prevChainHeight.Safe, observation.ChainHeight.Finalized)
	observation.ChainHeight.Latest = max(observation.ChainHeight.Latest, prevChainHeight.Latest, observation.ChainHeight.Safe)
}

func (rp *reportingPlugin) Observation(
	ctx context.Context,
	outctx ocr3types.OutcomeContext,
	rawQuery types.Query,
) (types.Observation, error) {
	var query ctypes.Query
	if err := proto.Unmarshal(rawQuery, &query); err != nil {
		return nil, fmt.Errorf("failed to unmarshal request IDs: %w", err)
	}

	if len(query.RequestIDs) > rp.config.MaxAllowedBatchSize {
		return nil, fmt.Errorf("too many request IDs: got %d, expected %d", len(query.RequestIDs), rp.config.MaxAllowedBatchSize)
	}

	observation := &ctypes.Observation{ChainHeight: &ctypes.ChainHeight{}, Observations: make(map[string]*ctypes.RequestObservation, len(query.RequestIDs))}
	var err error
	observation.ChainHeight.Finalized, err = rp.blocksProvider.GetFinalized()
	if err != nil {
		return nil, fmt.Errorf("failed to get finalized block height: %w", err)
	}

	observation.ChainHeight.Safe, err = rp.blocksProvider.GetSafe()
	if err != nil {
		return nil, fmt.Errorf("failed to get safe block height: %w", err)
	}

	observation.ChainHeight.Latest, err = rp.blocksProvider.GetLatest()
	if err != nil {
		return nil, fmt.Errorf("failed to get latest block height: %w", err)
	}

	rp.populateHeightFromPreviousOutcome(observation, outctx)

	err = validateChainHeight(observation.ChainHeight)
	if err != nil {
		return nil, fmt.Errorf("invalid chain height: %w", err)
	}

	rp.logger.Infow("Captures chain observations",
		"finalized", observation.ChainHeight.Finalized,
		"safe", observation.ChainHeight.Safe,
		"latest", observation.ChainHeight.Latest)

	for _, requestID := range query.RequestIDs {
		request, ok := rp.requestsStore.GetRequest(requestID)
		if !ok {
			continue
		}

		err := rp.observeRequest(observation, request)
		if err != nil {
			return nil, fmt.Errorf("failed to observe request: %w", err)
		}
	}

	rp.logger.Debugw("Observation complete", "observation", observation)

	return proto.Marshal(observation)
}

func (rp *reportingPlugin) observeRequest(observation *ctypes.Observation, rawRequest ctypes.Request) error {
	switch rq := rawRequest.(type) {
	case *ctypes.AggregatableRequest:
		panic("not implemented")
	case *ctypes.EventuallyConsistentRequest:
		requestOb, ok := rq.GetObservation()
		if !ok {
			return nil
		}
		observation.Observations[rq.ID()] = &ctypes.RequestObservation{
			Observation: &ctypes.RequestObservation_EventuallyConsistent{EventuallyConsistent: requestOb},
		}
		return nil
	case *ctypes.LockableToBlockRequest:
		observation.Observations[rq.ID()] = &ctypes.RequestObservation{
			Observation: &ctypes.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}},
		}
		return nil
	default:
		return fmt.Errorf("unsupported request type: %T", rq)
	}
}

func (rp *reportingPlugin) ValidateObservation(_ context.Context, outctx ocr3types.OutcomeContext, query types.Query, ao types.AttributedObservation) error {
	ob := new(ctypes.Observation)
	if err := proto.Unmarshal(ao.Observation, ob); err != nil {
		return fmt.Errorf("could not unmarshal proposed observation: %w", err)
	}

	err := validateChainHeight(ob.ChainHeight)
	if err != nil {
		return fmt.Errorf("invalid chain height: %w", err)
	}

	err = rp.validateExternalObservationAgainsOutcome(ob, outctx)
	if err != nil {
		return fmt.Errorf("observation contradicts prev outcome: %w", err)
	}

	return nil
}

func (rp *reportingPlugin) validateExternalObservationAgainsOutcome(ob *ctypes.Observation, outctx ocr3types.OutcomeContext) error {
	if len(outctx.PreviousOutcome) == 0 {
		return nil
	}

	var prev ctypes.Outcome
	err := proto.Unmarshal(outctx.PreviousOutcome, &prev)
	if err != nil {
		rp.logger.Errorw("Could not unmarshal previous outcome", "err", err, "previousOutcome", hex.EncodeToString(outctx.PreviousOutcome))
		return nil
	}

	if prev.ChainHeight == nil {
		return nil
	}

	err = validateChainHeightAgainstOutcome(ob.ChainHeight, prev.ChainHeight)
	if err != nil {
		return fmt.Errorf("invalid chain height compared to previous outcome: %w", err)
	}

	return nil
}

func validateChainHeight(chainHeight *ctypes.ChainHeight) error {
	if chainHeight == nil {
		return fmt.Errorf("chain height is nil")
	}
	if chainHeight.Latest < chainHeight.Safe {
		return fmt.Errorf("expected latest %d to be gt or equal to safe %d", chainHeight.Latest, chainHeight.Safe)
	}
	if chainHeight.Safe < chainHeight.Finalized {
		return fmt.Errorf("expected safe %d to be gt or equal to finalized %d", chainHeight.Safe, chainHeight.Finalized)
	}

	return nil
}

func fPlusOneLowestBlockHeight(obs []attributedObservation, f int, getHeight func(ob *ctypes.ChainHeight) int64) int64 {
	sort.Slice(obs, func(i, j int) bool {
		return getHeight(obs[i].Observation.ChainHeight) < getHeight(obs[j].Observation.ChainHeight)
	})

	return getHeight(obs[f].Observation.ChainHeight)
}

func validateChainHeightAgainstOutcome(ob *ctypes.ChainHeight, prevOutcome *ctypes.ChainHeight) error {
	if ob.Latest < prevOutcome.Latest {
		return fmt.Errorf("expected latest %d to be gt or equal to previous latest %d", ob.Latest, prevOutcome.Latest)
	}
	if ob.Safe < prevOutcome.Safe {
		return fmt.Errorf("expected safe %d to be gt or equal to previous safe %d", ob.Safe, prevOutcome.Safe)
	}
	if ob.Finalized < prevOutcome.Finalized {
		return fmt.Errorf("expected finalized %d to be gt or equal to previous finalized %d", ob.Finalized, prevOutcome.Finalized)
	}

	return nil
}

func (rp *reportingPlugin) ObservationQuorum(_ context.Context, outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (quorumReached bool, err error) {
	return quorumhelper.ObservationCountReachesObservationQuorum(quorumhelper.QuorumNMinusF, rp.config.N, rp.config.F, aos), nil
}

func (rp *reportingPlugin) agreeOnRequestType(requestID string, aos []attributedObservation) (ctypes.RequestType, error) {
	iterator := func(yield func(commontypes.OracleID, observation[ctypes.RequestType, ctypes.RequestType]) bool) {
		for _, ob := range aos {
			requestOb, ok := ob.Observation.Observations[requestID]
			if !ok || requestOb == nil {
				continue
			}
			var requestType ctypes.RequestType
			switch requestOb.GetObservation().(type) {
			case *ctypes.RequestObservation_EventuallyConsistent:
				requestType = ctypes.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT
			case *ctypes.RequestObservation_LockableToBlock:
				requestType = ctypes.RequestType_REQUEST_TYPE_LOCKABLE_TO_BLOCK
			case *ctypes.RequestObservation_Aggregatable:
				requestType = ctypes.RequestType_REQUEST_TYPE_AGGREGATABLE
			}
			yield(ob.Observer, observation[ctypes.RequestType, ctypes.RequestType]{
				Key:   requestType,
				Value: requestType,
			})
		}
	}

	return mode[ctypes.RequestType, ctypes.RequestType](rp.config.N, rp.config.F, iterator)
}

func (rp *reportingPlugin) agreeOnEventuallyConsistentValue(requestID string, aos []attributedObservation) ([]byte, error) {
	iterator := func(yield func(commontypes.OracleID, observation[[32]byte, []byte]) bool) {
		for _, ob := range aos {
			requestOb, ok := ob.Observation.Observations[requestID]
			if !ok || requestOb == nil {
				continue
			}

			key := sha256.Sum256(requestOb.GetEventuallyConsistent())
			yield(ob.Observer, observation[[32]byte, []byte]{
				Key:   key,
				Value: requestOb.GetEventuallyConsistent(),
			})
		}
	}

	return mode[[32]byte, []byte](rp.config.N, rp.config.F, iterator)
}

func (rp *reportingPlugin) agreeOnChainHeight(aos []attributedObservation) (*ctypes.ChainHeight, error) {
	if len(aos) < rp.config.F+1 {
		return nil, fmt.Errorf("not enough observations to calculate chain height. Got %d, expected at least %d", len(aos), rp.config.F+1)
	}

	return &ctypes.ChainHeight{
		Latest:    fPlusOneLowestBlockHeight(aos, rp.config.F, func(o *ctypes.ChainHeight) int64 { return o.Latest }),
		Safe:      fPlusOneLowestBlockHeight(aos, rp.config.F, func(o *ctypes.ChainHeight) int64 { return o.Safe }),
		Finalized: fPlusOneLowestBlockHeight(aos, rp.config.F, func(o *ctypes.ChainHeight) int64 { return o.Finalized }),
	}, nil
}

type attributedObservation struct {
	Observer    commontypes.OracleID
	Observation *ctypes.Observation
}

func (rp *reportingPlugin) Outcome(
	_ context.Context,
	outctx ocr3types.OutcomeContext,
	rawQuery types.Query,
	rawAOs []types.AttributedObservation,
) (ocr3types.Outcome, error) {
	aos := make([]attributedObservation, len(rawAOs))
	for i, ao := range rawAOs {
		aos[i] = attributedObservation{
			Observer:    ao.Observer,
			Observation: new(ctypes.Observation),
		}
		if err := proto.Unmarshal(ao.Observation, aos[i].Observation); err != nil {
			return nil, fmt.Errorf("could not unmarshal proposed observation: %w", err)
		}
	}

	outcome := ctypes.Outcome{ChainHeight: &ctypes.ChainHeight{}}
	// TODO PLEX-1572: report common chain height
	var err error
	outcome.ChainHeight, err = rp.agreeOnChainHeight(aos)
	if err != nil {
		return nil, fmt.Errorf("could not determine chain height: %w", err)
	}

	var query ctypes.Query
	if err := proto.Unmarshal(rawQuery, &query); err != nil {
		return nil, fmt.Errorf("failed to unmarshal request IDs: %w", err)
	}

	for _, requestID := range query.RequestIDs {
		requestType, err := rp.agreeOnRequestType(requestID, aos)
		if err != nil {
			rp.logger.Infow("Could not determine request type", "requestID", requestID, "err", err)
			continue
		}

		switch requestType {
		case ctypes.RequestType_REQUEST_TYPE_AGGREGATABLE:
			// TODO: PLEX-1470 implement aggregatable methods
			panic("not implemented")
		case ctypes.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT:
			value, err := rp.agreeOnEventuallyConsistentValue(requestID, aos)
			if err != nil {
				rp.logger.Infow("Could not determine request value", "requestID", requestID, "err", err)
				continue
			}
			outcome.Outcomes = append(outcome.Outcomes, &ctypes.RequestOutcome{
				RequestID: requestID,
				Outcome:   &ctypes.RequestOutcome_EventuallyConsistent{EventuallyConsistent: value},
			})
		case ctypes.RequestType_REQUEST_TYPE_LOCKABLE_TO_BLOCK:
			outcome.Outcomes = append(outcome.Outcomes, &ctypes.RequestOutcome{
				RequestID: requestID,
				Outcome:   &ctypes.RequestOutcome_LockableToBlock{LockableToBlock: &emptypb.Empty{}},
			})
		default:
			return nil, fmt.Errorf("unsupported request type: %s", requestType)
		}
	}

	return proto.Marshal(&outcome)
}

func (rp *reportingPlugin) Reports(ctx context.Context, seqNr uint64, rawOutcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	var outcome ctypes.Outcome
	if err := proto.Unmarshal(rawOutcome, &outcome); err != nil {
		return nil, fmt.Errorf("could not unmarshal proposed outcome: %w", err)
	}

	reports := make([]ocr3types.ReportPlus[[]byte], len(outcome.Outcomes))
	for i, requestOutcome := range outcome.Outcomes {
		report := ctypes.RequestReport{
			RequestID: requestOutcome.RequestID,
		}

		switch requestOutcome.Outcome.(type) {
		case *ctypes.RequestOutcome_EventuallyConsistent:
			report.Report = &ctypes.RequestReport_EventuallyConsistent{EventuallyConsistent: requestOutcome.GetEventuallyConsistent()}
		case *ctypes.RequestOutcome_LockableToBlock:
			report.Report = &ctypes.RequestReport_LockableToBlock{LockableToBlock: outcome.ChainHeight}
		default:
			return nil, fmt.Errorf("unsupported request type: %T", requestOutcome.Outcome)
		}

		asProto, err := proto.Marshal(&report)
		if err != nil {
			return nil, fmt.Errorf("could not marshal request report: %w", err)
		}
		reports[i] = ocr3types.ReportPlus[[]byte]{ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{Report: asProto}}
	}
	return reports, nil
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
