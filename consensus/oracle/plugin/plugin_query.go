package plugin

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/consensus/oracle"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
)

func (r *reportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	allRequests, err := r.store.All()
	if err != nil {
		return nil, fmt.Errorf("failed to get all requests: %w", err)
	}

	// Get only those requests that are pending consensus to prevent completed requests being included in the new query
	pendingRequests, err := r.getPendingRequests(outctx, allRequests)
	if err != nil {
		return nil, fmt.Errorf("failed to remove completed requests: %w", err)
	}

	// Take the first batchSize requests after filtering out completed requests
	if len(pendingRequests) > r.batchSize {
		pendingRequests = pendingRequests[:r.batchSize]
	}

	// To achieve a deterministic Outcome requires that each node has access to the same set of request observations, defaults and
	// consensus descriptors. Variations in the latter 2 would result in different nodes producing different outcomes.  As
	// the order of arrival of requests at each node is non-deterministic, relying on the request set at each node to provide
	// the consensus descriptors for a node would result in different nodes producing different outcomes for the same set
	// of observations.
	//
	// The solution to this problem is to embed the consensus descriptor and default into the query.  With this done, all nodes
	// will have access to the same consensus descriptor set and default when calculating the outcome.  One issue with this is that
	// it would allow the leader node to unduly influence the outcome by choosing which consensus descriptor and/or default to associate with
	// a request in the query.
	// To prevent this each node checks the consensus descriptor and default for a request in the query against the consensus descriptor
	// and default it has for the request and only contributes an observation for the request if they match.
	//
	// The same reasoning applies to the metadata for the request, which is also included in the query.

	seenIDs := make(map[IDKey]bool)
	cachedQuerySize := 0

	var reqs []*oracletypes.Request
	for _, rq := range pendingRequests {
		key := GetIDKey(rq)

		// Simple duplicate elimination using a map
		if seenIDs[key] {
			continue
		}

		serialisedConsensusDescriptor, err := proto.MarshalOptions{Deterministic: true}.Marshal(rq.Input.Descriptors)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal consensus descriptor for request %s: %w", rq.ID(), err)
		}

		isDefaultNil, err := isNilOrEmptySlice(rq.Input.Default)
		if err != nil {
			return nil, fmt.Errorf("failed to check if default is nil for request %s: %w", rq.ID(), err)
		}

		var serialisedDefault []byte
		if !isDefaultNil {
			serialisedDefault, err = proto.MarshalOptions{Deterministic: true}.Marshal(rq.Input.Default)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal default for request %s: %w", rq.ID(), err)
			}
		}

		newReq := &oracletypes.Request{
			Metadata:                   ToRequestMetaData(rq.Metadata),
			RequestConsensusDescriptor: serialisedConsensusDescriptor,
			RequestDefault:             serialisedDefault,
		}

		// If the new id would exceed the max query size, stop adding more ids
		ok, newSize := BatchHasCapacity(cachedQuerySize, newReq, int(r.config.MaxQueryLengthBytes),
			func() { r.metrics.IncBatchRequestsTotal(ctx, "query") },
			func() { r.metrics.IncBatchCapacityExceeded(ctx, "query") })
		if !ok {
			break
		}

		seenIDs[key] = true
		reqs = append(reqs, newReq)
		cachedQuerySize = newSize
	}

	r.lggr.Debugw("consensus plugin query complete", "number of requests", len(reqs))
	return proto.MarshalOptions{Deterministic: true}.Marshal(&oracletypes.Query{
		Requests: reqs,
	})
}

// Removes any requests that have already been completed (successfully/failed/errored) from the batch
// leaving only those requests that are pending consensus.
func (r *reportingPlugin) getPendingRequests(outctx ocr3types.OutcomeContext, allRequests []*oracle.ConsensusRequest) ([]*oracle.ConsensusRequest, error) {
	var pendingRequests []*oracle.ConsensusRequest
	if outctx.PreviousOutcome == nil {
		return allRequests, nil
	}

	prevOutcome := &oracletypes.Outcome{}
	err := proto.Unmarshal(outctx.PreviousOutcome, prevOutcome)
	if err != nil {
		r.lggr.Errorw("could not unmarshal previous outcome", "error", err)
		return nil, err
	}

	// Remove any requests from the batch that are already in the previous outcome and not marked as pending
	// This ensures that requests that have been completed (whether successfully/failed/errored) are not included in the new query
	requestIDToHistoricalOutcome := make(map[string]*oracletypes.HistoricalRequestOutcome)
	for _, ro := range prevOutcome.HistoricalOutcomes {
		requestIDToHistoricalOutcome[ro.RequestId] = ro
	}

	for _, rq := range allRequests {
		previousRequestOutcome, exists := requestIDToHistoricalOutcome[rq.ID()]
		// If the request ID exists in the historical outcome and is not marked as pending, skip it
		if exists && previousRequestOutcome.Status != oracletypes.RequestStatus_REQUEST_STATUS_CONSENSUS_PENDING {
			continue
		}

		pendingRequests = append(pendingRequests, rq)
	}

	return pendingRequests, nil
}
