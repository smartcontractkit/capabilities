package plugin

import (
	"context"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/consensus/oracle/plugin/batching"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
)

// Observation processes the query and returns the observation for the reporting plugin.  If a request for a given
// request ID is not found locally, it is simply skipped in the observation.
func (r *reportingPlugin) Observation(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query) (types.Observation, error) {
	requestsQuery := &oracletypes.Query{}
	err := proto.Unmarshal(query, requestsQuery)
	if err != nil {
		return nil, err
	}

	localRequests := r.store.GetByIDs(requestsQuery.RequestIDs)

	observationBatch := batching.NewObservationBatch(ctx, r.lggr, int(r.config.MaxObservationLengthBytes), r.metrics)

	for _, req := range localRequests {
		reqObs := &oracletypes.RequestObservation{
			Metadata:   ToRequestMetaData(req.Metadata),
			ReceivedAt: timestamppb.New(req.ReceivedAt),
			Input:      req.Input,
		}

		hasCapacity := observationBatch.AddObservation(ctx, reqObs)
		if !hasCapacity {
			r.lggr.Debugw("batch does not have capacity to add observation - skipping in this round", "requestID", reqObs.Metadata.RequestId)
			break
		}
	}

	r.lggr.Debugw("consensus plugin observation complete", "numObservations", observationBatch.NumObservationsInBatch(), "numOfRequestsInQuery", len(requestsQuery.RequestIDs))
	return observationBatch.SerialiseObservationBatch(ctx)
}
