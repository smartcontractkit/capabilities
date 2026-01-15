package oracle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/shopspring/decimal"
	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/smartcontractkit/libocr/quorumhelper"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const (
	// OCRRoundBatchSize - max number of requests that this node will try to process in a single round
	// TODO PLEX-1569: make configurable
	OCRRoundBatchSize = 200
	// OCRRoundMaxBatchSize - defines max number of requests that this node will process in a round, if requested by another node.
	// Needed to allow graceful roll out of OCRBatchSize increase.
	OCRRoundMaxBatchSize = 1000
)

var _ ocr3types.ReportingPlugin[[]byte] = (*reportingPlugin)(nil)

type Config struct {
	ocr3types.ReportingPluginConfig
	MaxBatchSize         int // max number of requests that this node will try to process in a single round
	MaxObservationLength int // max length of observation in bytes
}

type reportingPlugin struct {
	config         Config
	logger         logger.SugaredLogger
	blocksProvider BlocksProvider
	requestsStore  RequestsHandler
	metrics        metrics.ConsensusMetrics
}

func newReportingPlugin(
	config Config,
	logger logger.SugaredLogger,
	blocksProvider BlocksProvider,
	requestsStore RequestsHandler,
	metrics metrics.ConsensusMetrics,
) *reportingPlugin {
	return &reportingPlugin{
		config:         config,
		logger:         logger,
		blocksProvider: blocksProvider,
		requestsStore:  requestsStore,
		metrics:        metrics,
	}
}

func (rp *reportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	ids, err := rp.requestsStore.GetRequestIDs(rp.config.MaxBatchSize)
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

	if len(query.RequestIDs) > rp.config.MaxBatchSize {
		return nil, fmt.Errorf("too many request IDs: got %d, expected %d", len(query.RequestIDs), rp.config.MaxBatchSize)
	}

	chainHeight := ctypes.ChainHeight{
		Finalized: rp.blocksProvider.GetFinalized(),
		Safe:      rp.blocksProvider.GetSafe(),
		Latest:    rp.blocksProvider.GetLatest(),
	}
	observation := &ctypes.Observation{ChainHeight: &chainHeight, Observations: make(map[string]*ctypes.RequestObservation, len(query.RequestIDs))}

	rp.populateHeightFromPreviousOutcome(observation, outctx)

	err := validateChainHeight(observation.ChainHeight)
	if err != nil {
		return nil, fmt.Errorf("invalid chain height: %w", err)
	}

	rp.logger.Infow("Captures chain observations",
		"finalized", observation.ChainHeight.Finalized,
		"safe", observation.ChainHeight.Safe,
		"latest", observation.ChainHeight.Latest)

	const observationFieldProtoKey = 2
	currentSize := proto.Size(observation)
	for i, requestID := range query.RequestIDs {
		request, ok := rp.requestsStore.GetRequest(requestID)
		if !ok {
			continue
		}

		reqObservation, err := rp.getObservationForRequest(request)
		if err != nil {
			return nil, fmt.Errorf("failed to observe request: %w", err)
		}

		if reqObservation == nil {
			rp.logger.Debugw("No observation for request", "requestID", requestID)
			continue
		}

		newSize, ok := hasCapacityToAdd(currentSize, observationFieldProtoKey, requestID, reqObservation, rp.config.MaxObservationLength)
		if !ok {
			rp.logger.Info("Observation exceeds max size, skipping request", "id", requestID, "request_in_batch", len(query.RequestIDs), "requests_added", i+1, "currentSize", currentSize, "newSize", newSize)
			continue
		}

		requestApproxSize := newSize - currentSize
		rp.metrics.RecordRequestObservationSize(ctx, requestApproxSize)

		currentSize = newSize
		observation.Observations[requestID] = reqObservation
	}

	rp.logger.Debugw("Observation complete", "observation", observation)

	rawObservation, err := proto.Marshal(observation)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal observation: %w", err)
	}

	rp.metrics.RecordRoundObservationSize(ctx, len(rawObservation))
	return rawObservation, nil
}

func (rp *reportingPlugin) getObservationForRequest(rawRequest ctypes.Request) (*ctypes.RequestObservation, error) {
	switch rq := rawRequest.(type) {
	case *ctypes.AggregatableRequest:
		requestOb, observationErr, ok := rq.GetObservation()
		if !ok {
			return nil, nil
		}
		if observationErr != nil {
			return &ctypes.RequestObservation{
				Observation: &ctypes.RequestObservation_Error{Error: observationErr},
			}, nil
		}
		return &ctypes.RequestObservation{
			Observation: &ctypes.RequestObservation_Aggregatable{Aggregatable: requestOb},
		}, nil

	case *ctypes.EventuallyConsistentRequest:
		requestOb, observationErr, ok := rq.GetObservation()
		if !ok {
			return nil, nil
		}
		if observationErr != nil {
			return &ctypes.RequestObservation{
				Observation: &ctypes.RequestObservation_Error{Error: observationErr},
			}, nil
		}
		return &ctypes.RequestObservation{
			Observation: &ctypes.RequestObservation_EventuallyConsistent{EventuallyConsistent: requestOb},
		}, nil

	case *ctypes.LockableToBlockRequest:
		return &ctypes.RequestObservation{
			Observation: &ctypes.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported observation type: %T", rq)
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

	err = rp.validateExternalObservationAgainstOutcome(ob, outctx)
	if err != nil {
		return fmt.Errorf("observation contradicts prev outcome: %w", err)
	}

	return nil
}

func (rp *reportingPlugin) validateExternalObservationAgainstOutcome(ob *ctypes.Observation, outctx ocr3types.OutcomeContext) error {
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

func (rp *reportingPlugin) agreeOnObservationType(requestID string, aos []attributedObservation) (ctypes.ObservationType, error) {
	iterator := func(yield func(commontypes.OracleID, observation[ctypes.ObservationType, ctypes.ObservationType]) bool) {
		for _, ob := range aos {
			requestOb, ok := ob.Observation.Observations[requestID]
			if !ok || requestOb == nil {
				continue
			}
			var observationType ctypes.ObservationType
			switch requestOb.GetObservation().(type) {
			case *ctypes.RequestObservation_EventuallyConsistent:
				observationType = ctypes.ObservationType_EVENTUALLY_CONSISTENT
			case *ctypes.RequestObservation_LockableToBlock:
				observationType = ctypes.ObservationType_LOCKABLE_TO_BLOCK
			case *ctypes.RequestObservation_Aggregatable:
				observationType = ctypes.ObservationType_AGGREGATABLE
			case *ctypes.RequestObservation_Error:
				observationType = ctypes.ObservationType_ERROR
			}
			yield(ob.Observer, observation[ctypes.ObservationType, ctypes.ObservationType]{
				Key:   observationType,
				Value: observationType,
			})
		}
	}

	return mode[ctypes.ObservationType, ctypes.ObservationType](rp.config.N, rp.config.F, iterator)
}

func (rp *reportingPlugin) aggregateValue(requestID string, aos []attributedObservation) (*pb.Decimal, error) {
	aggrMethod, err := rp.agreeOnAggregationMethod(requestID, aos)
	if err != nil {
		return nil, fmt.Errorf("could not determine aggregation method: %w", err)
	}

	values := make([]decimal.Decimal, 0, len(aos))
	for _, ob := range aos {
		requestOb, ok := ob.Observation.Observations[requestID]
		if !ok || requestOb == nil {
			continue
		}

		aggrOb := requestOb.GetAggregatable()
		if aggrOb == nil || aggrOb.Value == nil || aggrOb.Value.Coefficient == nil {
			continue
		}

		values = append(values, decimal.NewFromBigInt(pb.NewIntFromBigInt(aggrOb.Value.Coefficient), aggrOb.Value.Exponent))
	}

	byzQuorum := byzQuorumSize(rp.config.N, rp.config.F)
	if len(values) < byzQuorum {
		return nil, fmt.Errorf("not enough observations to aggregate value. Got %d, expected at least %d", len(values), byzQuorum)
	}

	sort.Slice(values, func(i, j int) bool {
		return values[i].LessThan(values[j])
	})

	switch aggrMethod {
	case ctypes.AggregationMethodFPlusOneHighest:
		result := values[len(values)-(rp.config.F+1)]
		return &pb.Decimal{
			Coefficient: pb.NewBigIntFromInt(result.Coefficient()),
			Exponent:    result.Exponent(),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported aggregation method: %s", aggrMethod)
	}
}

func (rp *reportingPlugin) agreeOnAggregationMethod(requestID string, aos []attributedObservation) (string, error) {
	iterator := func(yield func(commontypes.OracleID, observation[string, string]) bool) {
		for _, ob := range aos {
			requestOb, ok := ob.Observation.Observations[requestID]
			if !ok || requestOb == nil {
				continue
			}
			aggrOb := requestOb.GetAggregatable()
			if aggrOb == nil {
				continue
			}
			yield(ob.Observer, observation[string, string]{
				Key:   aggrOb.Method,
				Value: aggrOb.Method,
			})
		}
	}

	return mode[string, string](rp.config.N, rp.config.F, iterator)
}

func (rp *reportingPlugin) agreeOnEventuallyConsistentValue(requestID string, aos []attributedObservation) ([]byte, error) {
	iterator := func(yield func(commontypes.OracleID, observation[[32]byte, []byte]) bool) {
		for _, ob := range aos {
			requestOb, ok := ob.Observation.Observations[requestID]
			if !ok || requestOb == nil {
				continue
			}

			if _, ok := requestOb.Observation.(*ctypes.RequestObservation_EventuallyConsistent); !ok {
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
		observationType, err := rp.agreeOnObservationType(requestID, aos)
		if err != nil {
			rp.logger.Infow("Could not determine observation type", "requestID", requestID, "err", err)
			continue
		}

		switch observationType {
		case ctypes.ObservationType_AGGREGATABLE:
			value, err := rp.aggregateValue(requestID, aos)
			if err != nil {
				rp.logger.Infow("Could not determine request value", "requestID", requestID, "err", err)
				continue
			}

			outcome.Outcomes = append(outcome.Outcomes, &ctypes.RequestOutcome{
				RequestID: requestID,
				Outcome:   &ctypes.RequestOutcome_Aggregatable{Aggregatable: value},
			})
		case ctypes.ObservationType_EVENTUALLY_CONSISTENT:
			value, err := rp.agreeOnEventuallyConsistentValue(requestID, aos)
			if err != nil {
				rp.logger.Infow("Could not determine request value", "requestID", requestID, "err", err)
				continue
			}
			outcome.Outcomes = append(outcome.Outcomes, &ctypes.RequestOutcome{
				RequestID: requestID,
				Outcome:   &ctypes.RequestOutcome_EventuallyConsistent{EventuallyConsistent: value},
			})
		case ctypes.ObservationType_LOCKABLE_TO_BLOCK:
			outcome.Outcomes = append(outcome.Outcomes, &ctypes.RequestOutcome{
				RequestID: requestID,
				Outcome:   &ctypes.RequestOutcome_LockableToBlock{LockableToBlock: &emptypb.Empty{}},
			})
		case ctypes.ObservationType_ERROR:
			requestErrors, err := modeForError(rp.config.N, rp.config.F, requestID, aos)
			if err != nil {
				rp.logger.Infow("Could not determine request error", "requestID", requestID, "err", err)
				continue
			}
			outcome.Outcomes = append(outcome.Outcomes, &ctypes.RequestOutcome{
				RequestID: requestID,
				Outcome:   &ctypes.RequestOutcome_Error{Error: &ctypes.RequestError{Errors: requestErrors}},
			})
		default:
			return nil, fmt.Errorf("unsupported observation type: %s", observationType)
		}
	}

	return proto.Marshal(&outcome)
}

func (rp *reportingPlugin) Reports(ctx context.Context, seqNr uint64, rawOutcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	var outcome ctypes.Outcome
	if err := proto.Unmarshal(rawOutcome, &outcome); err != nil {
		return nil, fmt.Errorf("could not unmarshal proposed outcome: %w", err)
	}

	rp.metrics.RecordOutcomeChainHeight(ctx, outcome.ChainHeight)

	reports := make([]ocr3types.ReportPlus[[]byte], len(outcome.Outcomes))
	for i, requestOutcome := range outcome.Outcomes {
		report := ctypes.RequestReport{
			RequestID: requestOutcome.RequestID,
		}

		switch requestOutcome.Outcome.(type) {
		case *ctypes.RequestOutcome_Aggregatable:
			report.Report = &ctypes.RequestReport_Aggregatable{Aggregatable: requestOutcome.GetAggregatable()}
		case *ctypes.RequestOutcome_EventuallyConsistent:
			report.Report = &ctypes.RequestReport_EventuallyConsistent{EventuallyConsistent: requestOutcome.GetEventuallyConsistent()}
		case *ctypes.RequestOutcome_LockableToBlock:
			report.Report = &ctypes.RequestReport_LockableToBlock{LockableToBlock: outcome.ChainHeight}
		case *ctypes.RequestOutcome_Error:
			report.Report = &ctypes.RequestReport_Error{Error: requestOutcome.GetError()}
		default:
			return nil, fmt.Errorf("unsupported observation type: %T", requestOutcome.Outcome)
		}

		asProto, err := proto.Marshal(&report)
		if err != nil {
			return nil, fmt.Errorf("could not marshal request report: %w", err)
		}
		info, err := createReportInfo()
		if err != nil {
			return nil, fmt.Errorf("failed to create report info for request, error: %w", err)
		}
		reports[i] = ocr3types.ReportPlus[[]byte]{
			ReportWithInfo: ocr3types.ReportWithInfo[[]byte]{
				Report: asProto,
				Info:   info,
			},
		}
	}
	return reports, nil
}

func createReportInfo() ([]byte, error) {
	infos, err := structpb.NewStruct(map[string]any{
		"keyBundleName": "evm",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create structpb for report info: %w", err)
	}
	infoBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(infos)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal report info: %w", err)
	}
	return infoBytes, nil
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
