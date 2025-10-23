package plugin

import (
	"context"
	"fmt"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

type timestampedObservation struct {
	Observation *valuespb.Value
	Timestamp   *timestamppb.Timestamp
}

func (r *reportingPlugin) Outcome(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query, attributedObservations []types.AttributedObservation) (ocr3types.Outcome, error) {
	requestsQuery := &oracletypes.Query{}
	err := proto.Unmarshal(query, requestsQuery)
	if err != nil {
		return nil, err
	}

	historicalOutcomes, requestIDToHistoricalOutcome, err := getNonExpiredHistoricalRequestOutcomes(r.lggr, outctx, r.outcomeExpirySeqNrSpan)
	if err != nil {
		return nil, fmt.Errorf("failed to get previous outcomes: %w", err)
	}

	requestIDToObservations := groupAttributedObservationsByRequestID(r.lggr, attributedObservations)

	var outcomes []*oracletypes.RequestOutcome
	cachedOutcomeSize := CalculateMessageSize(&oracletypes.Outcome{Outcomes: outcomes, HistoricalOutcomes: historicalOutcomes})
	for _, request := range requestsQuery.Requests {
		requestID := request.Metadata.RequestId
		observations := requestIDToObservations[requestID]

		consensusDescriptor := &sdk.ConsensusDescriptor{}
		err = proto.Unmarshal(request.RequestConsensusDescriptor, consensusDescriptor)
		if err != nil {
			return nil, fmt.Errorf("could not unmarshal consensus descriptor for request %s: %w", requestID, err)
		}

		values := make([]*valuespb.Value, 0, len(observations))
		timestamps := make([]*timestamppb.Timestamp, 0, len(observations))
		for _, obs := range observations {
			if obs.Observation == nil || obs.Timestamp == nil {
				r.lggr.Errorw("observation or timestamp is nil for request, skipping", "requestID", requestID)
				continue
			}

			timestamps = append(timestamps, obs.Timestamp)
			values = append(values, obs.Observation)
		}

		// Get the default value from the query request if it exists
		var defaultValue *valuespb.Value
		if request.RequestDefault != nil {
			defaultValue = &valuespb.Value{}
			err := proto.Unmarshal(request.RequestDefault, defaultValue)
			if err != nil {
				return nil, fmt.Errorf("could not unmarshal default value for request %s: %w", requestID, err)
			}
		}

		var requestOutcome *oracletypes.RequestOutcome
		var historicalRequestOutcome *oracletypes.HistoricalRequestOutcome
		value, err := oracle.CalculateOutcomeForObservations(r.lggr, values, consensusDescriptor, defaultValue, r.minimumObservations, r.f)
		if err != nil {
			// TODO - pending this JIRA https://smartcontract-it.atlassian.net/browse/CAPPL-1076 mark the request as
			// pending so it is included in the next round. Subsequent PR for the latter JIRA will address better consensus failure and
			// error handling separately to avoid unnecessary consensus retries for the request and address DoS (+allow request to fail fast if consensus is not possible).
			r.lggr.Errorw("failed to calculate outcome for observations", "requestID", requestID, "error", err)
			historicalRequestOutcome = &oracletypes.HistoricalRequestOutcome{
				RequestId:        request.Metadata.RequestId,
				Status:           oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_PENDING,
				FirstSeenAtSeqNr: outctx.SeqNr,
			}
		} else {
			serialisedValue, err := proto.MarshalOptions{Deterministic: true}.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal outcome value for request %s: %w", requestID, err)
			}
			requestOutcome = &oracletypes.RequestOutcome{
				Metadata:  request.Metadata,
				Outcome:   serialisedValue,
				Timestamp: calculateMedianTimestamp(timestamps),
				Status:    oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_SUCCESS,
			}

			historicalRequestOutcome = &oracletypes.HistoricalRequestOutcome{
				RequestId:        request.Metadata.RequestId,
				Status:           oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_SUCCESS,
				FirstSeenAtSeqNr: outctx.SeqNr,
			}
		}

		if existingHistoricalRequestOutcome, ok := requestIDToHistoricalOutcome[requestID]; ok {
			// If the request already exists in the historical outcomes update the status only
			existingHistoricalRequestOutcome.Status = historicalRequestOutcome.Status
			historicalRequestOutcome = nil
		}

		hasCapacity, newOutcomeSize := r.checkOutcomeBatchHasCapacity(ctx, cachedOutcomeSize, requestOutcome, historicalRequestOutcome)
		if !hasCapacity {
			break
		}

		cachedOutcomeSize = newOutcomeSize

		if requestOutcome != nil {
			outcomes = append(outcomes, requestOutcome)
		}

		if historicalRequestOutcome != nil {
			historicalOutcomes = append(historicalOutcomes, historicalRequestOutcome)
		}
	}

	serialisedOutcome, err := proto.MarshalOptions{Deterministic: true}.Marshal(&oracletypes.Outcome{
		Outcomes:           outcomes,
		HistoricalOutcomes: historicalOutcomes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal outcome: %w", err)
	}

	return serialisedOutcome, nil
}

func (r *reportingPlugin) checkOutcomeBatchHasCapacity(ctx context.Context, existingOutcomeSize int, requestOutcome *oracletypes.RequestOutcome,
	historicalRequestOutcome *oracletypes.HistoricalRequestOutcome) (bool, int) {
	if requestOutcome != nil {
		ok, newSize := BatchHasCapacity(existingOutcomeSize, requestOutcome, int(r.config.MaxOutcomeLengthBytes),
			func() { r.metrics.IncBatchRequestsTotal(ctx, "outcome") },
			func() { r.metrics.IncBatchCapacityExceeded(ctx, "outcome") })

		if !ok {
			r.lggr.Debugw("max outcome batch size reached, skipping other requests", "requestID", requestOutcome.Metadata.RequestId)
			return false, 0
		}

		existingOutcomeSize = newSize
	}

	if historicalRequestOutcome != nil {
		ok, newSize := BatchHasCapacity(existingOutcomeSize, historicalRequestOutcome, int(r.config.MaxOutcomeLengthBytes),
			func() { r.metrics.IncBatchRequestsTotal(ctx, "outcome") },
			func() { r.metrics.IncBatchCapacityExceeded(ctx, "outcome") })

		if !ok {
			r.lggr.Debugw("max outcome batch size reached when adding historical request outcome, skipping other requests", "requestID", historicalRequestOutcome.RequestId)
			return false, 0
		}

		existingOutcomeSize = newSize
	}

	return true, existingOutcomeSize
}

func getNonExpiredHistoricalRequestOutcomes(lggr logger.Logger, outctx ocr3types.OutcomeContext, outcomeExpirySeqNrSpan uint64) ([]*oracletypes.HistoricalRequestOutcome, map[string]*oracletypes.HistoricalRequestOutcome, error) {
	var nonExpiredHistoricalOutcomes []*oracletypes.HistoricalRequestOutcome
	requestIDToHistoricalOutcome := map[string]*oracletypes.HistoricalRequestOutcome{}
	if outctx.PreviousOutcome != nil {
		prevOutcome := &oracletypes.Outcome{}
		err := proto.Unmarshal(outctx.PreviousOutcome, prevOutcome)
		if err != nil {
			lggr.Errorw("could not unmarshal previous outcome", "error", err)
			return nil, nil, err
		}

		for _, ho := range prevOutcome.HistoricalOutcomes {
			if outctx.SeqNr-ho.FirstSeenAtSeqNr <= outcomeExpirySeqNrSpan {
				nonExpiredHistoricalOutcomes = append(nonExpiredHistoricalOutcomes, ho)
				requestIDToHistoricalOutcome[ho.RequestId] = ho
			}
		}
	}

	return nonExpiredHistoricalOutcomes, requestIDToHistoricalOutcome, nil
}

func groupAttributedObservationsByRequestID(lggr logger.Logger, attributedObservations []types.AttributedObservation) map[string][]timestampedObservation {
	requestIDToObservations := make(map[string][]timestampedObservation)
	for _, ao := range attributedObservations {
		obs := &oracletypes.Observation{}
		err := proto.Unmarshal(ao.Observation, obs)
		if err != nil {
			lggr.Errorw("could not unmarshal observation from observer", "error", err, "observer", ao.Observer)
			continue
		}

		for _, requestObservation := range obs.Observations {
			requestID := requestObservation.Metadata.RequestId
			observationValue := &valuespb.Value{}
			err = proto.Unmarshal(requestObservation.Observation, observationValue)
			if err != nil {
				lggr.Errorw("could not unmarshal observation for request from observer", "error", err, "requestID", requestID, "observer", ao.Observer)
				continue
			}

			// Check the observation correctly marshals to a value to ensure it is a valid observation
			_, err = values.FromProto(observationValue)
			if err != nil {
				lggr.Errorw("could not convert observation value proto to value", "error", err, "requestID", requestID, "observer", ao.Observer)
				continue
			}

			requestIDToObservations[requestID] = append(requestIDToObservations[requestID], timestampedObservation{
				Observation: observationValue,
				Timestamp:   requestObservation.ReceivedAt,
			})
		}
	}
	return requestIDToObservations
}

func calculateMedianTimestamp(timestamps []*timestamppb.Timestamp) *timestamppb.Timestamp {
	slices.SortFunc(timestamps, func(a, b *timestamppb.Timestamp) int {
		if a.AsTime().Before(b.AsTime()) {
			return -1
		}
		if a.AsTime().After(b.AsTime()) {
			return 1
		}
		return 0
	})
	timestampCount := len(timestamps)
	mid := timestampCount / 2

	finalTimestamp := timestamps[mid]
	if timestampCount%2 != 1 {
		a := timestamps[mid-1].AsTime().Unix()
		b := timestamps[mid].AsTime().Unix()
		// a + (b-a) / 2 to avoid overflows
		finalTimestamp = timestamppb.New(time.Unix(a+(b-a)/2, 0))
	}
	return finalTimestamp
}
