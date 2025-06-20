package oracle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/smartcontractkit/libocr/quorumhelper"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ ocr3types.ReportingPlugin[[]byte] = (*reportingPlugin)(nil)

type Config struct {
	ocr3types.ReportingPluginConfig
	BatchSize int // max number of requests to be handled in a single OCR round
}

type reportingPlugin struct {
	config         Config
	logger         logger.SugaredLogger
	blocksProvider BlocksProvider
	requestsStore  RequestsStore
}

func NewReportingPlugin(
	config Config,
	logger logger.SugaredLogger,
	blocksProvider BlocksProvider,
	requestsStore RequestsStore,
) ocr3types.ReportingPlugin[[]byte] {
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
	return json.Marshal(ids)
}

func (rp *reportingPlugin) Observation(
	ctx context.Context,
	outctx ocr3types.OutcomeContext,
	query types.Query,
) (types.Observation, error) {
	var requestIDs []string
	if err := json.Unmarshal(query, &requestIDs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal request IDs: %w", err)
	}

	if len(requestIDs) > rp.config.BatchSize {
		return nil, fmt.Errorf("too many request IDs: %d expected %d", len(requestIDs), rp.config.BatchSize)
	}

	observation := &evmservice.Observations{ChainHeight: &evmservice.ChainHeight{}}
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

	err = validateChainHeight(observation.ChainHeight)
	if err != nil {
		return nil, fmt.Errorf("invalid chain height: %w", err)
	}

	if len(outctx.PreviousOutcome) > 0 {
		var previousOutcome evmservice.Outcome
		err := proto.Unmarshal(outctx.PreviousOutcome, &previousOutcome)
		if err != nil {
			rp.logger.Errorw("Could not unmarshal previous outcome", "err", err, "previousOutcome", hex.EncodeToString(outctx.PreviousOutcome))
			return nil, fmt.Errorf("could not unmarshal previous outcome: %w", err)
		}

		prevChainHeight := previousOutcome.ChainHeight
		observation.ChainHeight.Finalized = max(observation.ChainHeight.Finalized, prevChainHeight.Finalized)
		observation.ChainHeight.Safe = max(observation.ChainHeight.Safe, prevChainHeight.Safe, observation.ChainHeight.Finalized)
		observation.ChainHeight.Latest = max(observation.ChainHeight.Latest, prevChainHeight.Latest, observation.ChainHeight.Safe)
	}

	rp.logger.Infow("Captures chain observations",
		"finalized", observation.ChainHeight.Finalized,
		"safe", observation.ChainHeight.Safe,
		"latest", observation.ChainHeight.Latest)

	for _, requestID := range requestIDs {
		request, ok := rp.requestsStore.GetRequest(requestID)
		if !ok {
			continue
		}

		requestType := request.Type()
		switch requestType {
		case evmservice.RequestType_REQUEST_TYPE_AGGREGATABLE, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT:
			requestOb, ok := rp.requestsStore.GetObservation(requestID)
			if !ok {
				continue
			}
			observation.Observations[requestID] = &evmservice.RequestObservation{
				RequestType: requestType,
				Value:       requestOb,
			}
		case evmservice.RequestType_REQUEST_TYPE_LOCKABLE_TO_BLOCK:
			observation.Observations[requestID] = &evmservice.RequestObservation{
				RequestType: requestType,
			}
		default:
			return nil, fmt.Errorf("unsupported request type: %s", requestType)
		}

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

	err := validateChainHeight(ob.ChainHeight)
	if err != nil {
		return fmt.Errorf("invalid chain height: %w", err)
	}

	if len(outctx.PreviousOutcome) > 0 {
		prev := new(evmservice.Outcome)
		err := proto.Unmarshal(outctx.PreviousOutcome, prev)
		if err != nil {
			rp.logger.Errorw("Could not unmarshal previous outcome", "err", err, "previousOutcome", hex.EncodeToString(outctx.PreviousOutcome))
			return fmt.Errorf("could not unmarshal previous outcome: %w", err)
		}

		if prev.ChainHeight != nil {
			err = validateChainHeightAgainstOutcome(ob.ChainHeight, prev.ChainHeight)
			if err != nil {
				return fmt.Errorf("invalid chain height compared to previous outcome: %w", err)
			}
		}
	}

	return nil
}

func validateChainHeight(chainHeight *evmservice.ChainHeight) error {
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

func fPlusOneLowestBlockHeight(obs []attributedObservation, f int, getHeight func(ob *evmservice.ChainHeight) int64) (int64, error) {
	if len(obs) < f+1 {
		return 0, fmt.Errorf("not enough observations to calculate F+1 lowest block height. Got %d, expected at least %d", len(obs), f+1)
	}
	sort.Slice(obs, func(i, j int) bool {
		return getHeight(obs[i].Observation.ChainHeight) < getHeight(obs[j].Observation.ChainHeight)
	})

	return getHeight(obs[f].Observation.ChainHeight), nil
}

func validateChainHeightAgainstOutcome(ob *evmservice.ChainHeight, prevOutcome *evmservice.ChainHeight) error {
	if ob.Latest < prevOutcome.Latest {
		return fmt.Errorf("expected latest to be gt or equal to previous latest: %d < %d", ob.Latest, prevOutcome.Latest)
	}
	if ob.Safe < prevOutcome.Safe {
		return fmt.Errorf("expected safe to be gt or equal to previous safe: %d < %d", ob.Safe, prevOutcome.Safe)
	}
	if ob.Finalized < prevOutcome.Finalized {
		return fmt.Errorf("expected finalized to be gt or equal to previous finalized: %d < %d", ob.Finalized, prevOutcome.Finalized)
	}

	return nil
}

func (rp *reportingPlugin) ObservationQuorum(_ context.Context, outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (quorumReached bool, err error) {
	return quorumhelper.ObservationCountReachesObservationQuorum(quorumhelper.QuorumByzQuorum, rp.config.N, rp.config.F, aos), nil
}

func (rp *reportingPlugin) agreeOnRequestType(requestID string, aos []attributedObservation) (evmservice.RequestType, error) {
	iterator := func(yield func(commontypes.OracleID, observation[evmservice.RequestType, evmservice.RequestType]) bool) {
		for _, ob := range aos {
			requestOb, ok := ob.Observation.Observations[requestID]
			if !ok || requestOb == nil {
				continue
			}
			yield(ob.Observer, observation[evmservice.RequestType, evmservice.RequestType]{
				Key:   requestOb.RequestType,
				Value: requestOb.RequestType,
			})
		}
	}

	return mode[evmservice.RequestType, evmservice.RequestType](rp.config.N, rp.config.F, iterator)
}

func (rp *reportingPlugin) agreeOnRequestValue(requestID string, aos []attributedObservation) ([]byte, error) {
	iterator := func(yield func(commontypes.OracleID, observation[[32]byte, []byte]) bool) {
		for _, ob := range aos {
			requestOb, ok := ob.Observation.Observations[requestID]
			if !ok || requestOb == nil {
				continue
			}

			key := sha256.Sum256(requestOb.Value)
			yield(ob.Observer, observation[[32]byte, []byte]{
				Key:   key,
				Value: requestOb.Value,
			})
		}
	}

	return mode[[32]byte, []byte](rp.config.N, rp.config.F, iterator)
}

func (rp *reportingPlugin) agreeOnChainHeight(aos []attributedObservation) (*evmservice.ChainHeight, error) {
	latest, err := fPlusOneLowestBlockHeight(aos, rp.config.F, func(o *evmservice.ChainHeight) int64 { return o.Latest })
	if err != nil {
		return nil, fmt.Errorf("could not determine latest block height: %w", err)
	}

	safe, err := fPlusOneLowestBlockHeight(aos, rp.config.F, func(o *evmservice.ChainHeight) int64 { return o.Safe })
	if err != nil {
		return nil, fmt.Errorf("could not determine safe block height: %w", err)
	}

	finalized, err := fPlusOneLowestBlockHeight(aos, rp.config.F, func(o *evmservice.ChainHeight) int64 { return o.Finalized })
	if err != nil {
		return nil, fmt.Errorf("could not determine finalized block height: %w", err)
	}

	return &evmservice.ChainHeight{
		Latest:    latest,
		Safe:      safe,
		Finalized: finalized,
	}, nil
}

type attributedObservation struct {
	Observer    commontypes.OracleID
	Observation *evmservice.Observations
}

func (rp *reportingPlugin) Outcome(
	_ context.Context,
	outctx ocr3types.OutcomeContext,
	query types.Query,
	rawAOs []types.AttributedObservation,
) (ocr3types.Outcome, error) {
	aos := make([]attributedObservation, len(rawAOs))
	for i, ao := range rawAOs {
		aos[i] = attributedObservation{
			Observer:    ao.Observer,
			Observation: new(evmservice.Observations),
		}
		if err := proto.Unmarshal(ao.Observation, aos[i].Observation); err != nil {
			return nil, fmt.Errorf("could not unmarshal proposed outcome: %w", err)
		}
	}

	outcome := evmservice.Outcome{ChainHeight: &evmservice.ChainHeight{}}
	var err error
	outcome.ChainHeight, err = rp.agreeOnChainHeight(aos)
	if err != nil {
		return nil, fmt.Errorf("could not determine chain height: %w", err)
	}

	var requestIDs []string
	if err := json.Unmarshal(query, &requestIDs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal request IDs: %w", err)
	}

	for _, requestID := range requestIDs {
		requestType, err := rp.agreeOnRequestType(requestID, aos)
		if err != nil {
			rp.logger.Infow("Could not determine request type", "requestID", requestID, "err", err)
			continue
		}

		switch requestType {
		case evmservice.RequestType_REQUEST_TYPE_AGGREGATABLE:
			// TODO: PLEX-1470
			panic("not implemented")
		case evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT:
			value, err := rp.agreeOnRequestValue(requestID, aos)
			if err != nil {
				rp.logger.Infow("Could not determine request value", "requestID", requestID, "err", err)
				continue
			}
			outcome.Outcomes = append(outcome.Outcomes, &evmservice.RequestOutcome{
				RequestID:   requestID,
				RequestType: requestType,
				Value:       value,
			})
		case evmservice.RequestType_REQUEST_TYPE_LOCKABLE_TO_BLOCK:
			outcome.Outcomes = append(outcome.Outcomes, &evmservice.RequestOutcome{
				RequestID:   requestID,
				RequestType: requestType,
			})
		default:
			return nil, fmt.Errorf("unsupported request type: %s", requestType)
		}
	}

	return proto.Marshal(&outcome)
}

func (rp *reportingPlugin) Reports(ctx context.Context, seqNr uint64, rawOutcome ocr3types.Outcome) ([]ocr3types.ReportPlus[[]byte], error) {
	var outcome evmservice.Outcome
	if err := proto.Unmarshal(rawOutcome, &outcome); err != nil {
		return nil, fmt.Errorf("could not unmarshal proposed outcome: %w", err)
	}

	reports := make([]ocr3types.ReportPlus[[]byte], len(outcome.Outcomes))
	for i, requestOutcome := range outcome.Outcomes {
		report := evmservice.RequestReport{
			RequestID:   requestOutcome.RequestID,
			RequestType: requestOutcome.RequestType,
		}

		switch requestOutcome.RequestType {
		case evmservice.RequestType_REQUEST_TYPE_AGGREGATABLE, evmservice.RequestType_REQUEST_TYPE_EVENTUALLY_CONSISTENT:
			report.Payload = &evmservice.RequestReport_Value{Value: requestOutcome.Value}
		case evmservice.RequestType_REQUEST_TYPE_LOCKABLE_TO_BLOCK:
			report.Payload = &evmservice.RequestReport_Height{Height: outcome.ChainHeight}
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
