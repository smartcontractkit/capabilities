package oracle

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/smartcontractkit/libocr/commontypes"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/test"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

const (
	fuzzN                 = 4
	fuzzF                 = 1
	fuzzMaxReportCount    = 3
	fuzzMaxReportLenBytes = 256
)

func newFuzzPlugin(t *testing.T) *reportingPlugin {
	t.Helper()
	cfg := Config{
		N: fuzzN, F: fuzzF,
		MaxBatchSize:         10,
		MaxObservationLength: 1 << 20,
		MaxReportLengthBytes: fuzzMaxReportLenBytes,
		MaxReportCount:       fuzzMaxReportCount,
	}
	p, err := newReportingPlugin(cfg, logger.Sugared(logger.Nop()), nil, nil, test.GetConsensusMetrics(t), limits.Factory{})
	require.NoError(t, err)
	return p
}

func seedFuzzObservation(t testing.TB, requestID string, ob *types.RequestObservation) []byte {
	b, err := proto.Marshal(&types.Observation{
		ChainHeight:  &types.ChainHeight{Latest: 10, Safe: 9, Finalized: 8},
		Observations: map[string]*types.RequestObservation{requestID: ob},
	})
	require.NoError(t, err)
	return b
}

func seedFuzzObservationFull(t testing.TB, height *types.ChainHeight, obs map[string]*types.RequestObservation, missing []string) []byte {
	b, err := proto.Marshal(&types.Observation{ChainHeight: height, Observations: obs, MissingRequestIDs: missing})
	require.NoError(t, err)
	return b
}

func fuzzQuery(t testing.TB, ids string) ocrtypes.Query {
	var requestIDs []string
	if ids != "" {
		requestIDs = strings.Split(strings.ToValidUTF8(ids, "?"), ",")
	}
	q, err := proto.Marshal(&types.Query{RequestIDs: requestIDs})
	require.NoError(t, err)
	return q
}

func FuzzReportingPlugin_OutcomeInvariants(f *testing.F) {
	hash := make([]byte, types.HashLength)
	seeds := []*types.RequestObservation{
		{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("v")}},
		{Observation: &types.RequestObservation_Hashable{Hashable: hash}},
		{Observation: &types.RequestObservation_Error{Error: []byte("boom")}},
		{Observation: &types.RequestObservation_LockableToBlock{LockableToBlock: &emptypb.Empty{}}},
		{Observation: &types.RequestObservation_Aggregatable{Aggregatable: &types.AggregatableObservation{
			Value:  newDecimal(5, 0),
			Method: types.AggregationMethodFPlusOneHighest,
		}}},
		{Observation: &types.RequestObservation_Volatile{Volatile: &types.VolatileObservations{
			Observations: []*types.VolatileObservation{{Height: 9, Hash: hash}},
		}}},
	}
	for _, s := range seeds {
		honest := seedFuzzObservation(f, "r1", s)
		f.Add([]byte{}, "r1", honest, honest, honest, []byte{})
	}
	prev, err := proto.Marshal(&types.Outcome{ChainHeight: &types.ChainHeight{Latest: 10, Safe: 9, Finalized: 8}})
	require.NoError(f, err)
	ec := seedFuzzObservation(f, "r1", seeds[0])
	f.Add(prev, "r1,r1", ec, ec, ec, ec)
	f.Add([]byte{0xff}, "", []byte{}, []byte{0x0a}, []byte{0xff, 0xff}, []byte{})

	recovering, err := proto.Marshal(&types.Observation{
		ChainHeight: &types.ChainHeight{Latest: 10, Safe: 9, Finalized: 8},
		Observations: map[string]*types.RequestObservation{
			"r2": {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("v")}},
		},
		EnableMissingRequestRecovery: true,
	})
	require.NoError(f, err)
	prevMissing, err := proto.Marshal(&types.Outcome{
		ChainHeight:       &types.ChainHeight{Latest: 10, Safe: 9, Finalized: 8},
		MissingRequestIDs: []string{"r2"},
	})
	require.NoError(f, err)
	f.Add(prevMissing, "r1", recovering, recovering, recovering, recovering)
	// the same recovery scenario without a quorum of votes must not recover r2
	notRecovering := seedFuzzObservationFull(f, &types.ChainHeight{Latest: 10, Safe: 9, Finalized: 8}, map[string]*types.RequestObservation{
		"r2": {Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte("v")}},
	}, nil)
	f.Add(prevMissing, "r1", recovering, recovering, notRecovering, notRecovering)

	ecObservation := func(ids ...string) map[string]*types.RequestObservation {
		obs := map[string]*types.RequestObservation{}
		for _, id := range ids {
			obs[id] = &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: []byte(id)}}
		}
		return obs
	}
	height := &types.ChainHeight{Latest: 10, Safe: 9, Finalized: 8}
	// more requests than MaxReportCount
	fiveRequests := seedFuzzObservationFull(f, height, ecObservation("r1", "r2", "r3", "r4", "r5"), nil)
	f.Add([]byte{}, "r1,r2,r3,r4,r5", fiveRequests, fiveRequests, fiveRequests, []byte{})
	// one report is larger than MaxReportLengthBytes and must be skipped
	oversized := ecObservation("r2")
	oversized["r1"] = &types.RequestObservation{Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: make([]byte, fuzzMaxReportLenBytes+1)}}
	oversizedObservation := seedFuzzObservationFull(f, height, oversized, nil)
	f.Add([]byte{}, "r1,r2", oversizedObservation, oversizedObservation, oversizedObservation, []byte{})
	// F+1 oracles agree on a missing request ID, one does not
	withMissing := seedFuzzObservationFull(f, height, ecObservation("r1"), []string{"m1"})
	withoutMissing := seedFuzzObservationFull(f, height, ecObservation("r1"), []string{"m2"})
	f.Add([]byte{}, "r1", withMissing, withMissing, withoutMissing, []byte{})
	// observers report different chain heights: the agreed height is the F+1-th lowest of each field
	heightObservation := func(latest, safe, finalized int64) []byte {
		return seedFuzzObservationFull(f, &types.ChainHeight{Latest: latest, Safe: safe, Finalized: finalized}, ecObservation("r1"), nil)
	}
	f.Add([]byte{}, "r1", heightObservation(10, 9, 8), heightObservation(12, 11, 10), heightObservation(20, 15, 12), heightObservation(30, 20, 15))

	f.Fuzz(func(t *testing.T, prevOutcome []byte, ids string, o0, o1, o2, o3 []byte) {
		query := fuzzQuery(t, ids)
		outctx := ocr3types.OutcomeContext{PreviousOutcome: prevOutcome}

		var aos []ocrtypes.AttributedObservation
		validator := newFuzzPlugin(t)
		for i, raw := range [][]byte{o0, o1, o2, o3} {
			ao := ocrtypes.AttributedObservation{Observation: raw, Observer: commontypes.OracleID(i)}
			if validator.ValidateObservation(t.Context(), outctx, query, ao) == nil {
				aos = append(aos, ao)
			}
		}
		// libocr only calls Outcome once the observation quorum is reached.
		quorum, err := validator.ObservationQuorum(t.Context(), outctx, query, aos)
		require.NoError(t, err)
		if !quorum {
			return
		}

		first, err1 := newFuzzPlugin(t).Outcome(t.Context(), outctx, query, aos)
		second, err2 := newFuzzPlugin(t).Outcome(t.Context(), outctx, query, aos)
		require.Equal(t, errString(err1), errString(err2))
		require.Equal(t, first, second, "same inputs must yield identical outcome")
		if err1 != nil {
			return
		}

		outcome := &types.Outcome{}
		require.NoError(t, proto.Unmarshal(first, outcome))
		require.NoError(t, validateChainHeight(outcome.ChainHeight), "agreed chain height must be valid")

		var q types.Query
		require.NoError(t, proto.Unmarshal(query, &q))
		requireOutcomeMatchesObservations(t, outcome, &q, prevOutcome, aos)

		reports, err := newFuzzPlugin(t).Reports(t.Context(), 1, first)
		require.NoError(t, err)
		wantReports := 0
		for _, ro := range outcome.Outcomes {
			if report, _, err := getReportAndInfo(outcome, ro); err == nil && len(report) <= fuzzMaxReportLenBytes {
				wantReports++
				if wantReports == fuzzMaxReportCount {
					break
				}
			}
		}
		require.Len(t, reports, wantReports, "reports must cover every outcome that fits, up to MaxReportCount")
		for _, r := range reports {
			require.LessOrEqual(t, len(r.ReportWithInfo.Report), fuzzMaxReportLenBytes)
		}
		again, err := newFuzzPlugin(t).Reports(t.Context(), 1, first)
		require.NoError(t, err)
		require.Equal(t, reports, again, "same outcome must yield identical reports")
	})
}

// requireOutcomeMatchesObservations checks the outcome against what the validated observations say, independently of
// how the plugin aggregates them: the chain height, the agreed missing requests, which requests may and must be
// resolved, and that agreed eventually consistent values are backed by F+1 matching observations.
func requireOutcomeMatchesObservations(t *testing.T, outcome *types.Outcome, q *types.Query, prevOutcome []byte, aos []ocrtypes.AttributedObservation) {
	t.Helper()
	observations := make([]*types.Observation, 0, len(aos))
	for _, ao := range aos {
		ob := &types.Observation{}
		require.NoError(t, proto.Unmarshal(ao.Observation, ob))
		observations = append(observations, ob)
	}

	fPlusOneLowest := func(field func(*types.ChainHeight) int64) int64 {
		values := make([]int64, 0, len(observations))
		for _, ob := range observations {
			values = append(values, field(ob.ChainHeight))
		}
		slices.Sort(values)
		return values[fuzzF]
	}
	require.Equal(t, fPlusOneLowest(func(h *types.ChainHeight) int64 { return h.Latest }), outcome.ChainHeight.Latest, "latest must be the F+1-th lowest")
	require.Equal(t, fPlusOneLowest(func(h *types.ChainHeight) int64 { return h.Safe }), outcome.ChainHeight.Safe, "safe must be the F+1-th lowest")
	require.Equal(t, fPlusOneLowest(func(h *types.ChainHeight) int64 { return h.Finalized }), outcome.ChainHeight.Finalized, "finalized must be the F+1-th lowest")

	var previous types.Outcome
	hasPrevious := len(prevOutcome) > 0 && proto.Unmarshal(prevOutcome, &previous) == nil
	if hasPrevious && previous.ChainHeight != nil {
		require.GreaterOrEqual(t, outcome.ChainHeight.Latest, previous.ChainHeight.Latest, "chain height must not go backwards")
		require.GreaterOrEqual(t, outcome.ChainHeight.Safe, previous.ChainHeight.Safe, "chain height must not go backwards")
		require.GreaterOrEqual(t, outcome.ChainHeight.Finalized, previous.ChainHeight.Finalized, "chain height must not go backwards")
	}

	missingVotes := map[string]int{}
	recoveryVotes := 0
	for _, ob := range observations {
		for _, id := range ob.MissingRequestIDs {
			missingVotes[id]++
		}
		if ob.EnableMissingRequestRecovery {
			recoveryVotes++
		}
	}
	var wantMissing []string
	for id, votes := range missingVotes {
		if votes > fuzzF {
			wantMissing = append(wantMissing, id)
		}
	}
	slices.Sort(wantMissing)
	require.True(t, slices.Equal(wantMissing, outcome.MissingRequestIDs), "missing requests must be those reported by more than F oracles, sorted: want %v, got %v", wantMissing, outcome.MissingRequestIDs)

	// Requests the previous outcome reported as missing are only resolved once a quorum enables recovery.
	candidates := slices.Clone(q.RequestIDs)
	if hasPrevious && recoveryVotes >= byzQuorumSize(fuzzN, fuzzF) {
		candidates = append(candidates, previous.MissingRequestIDs...)
	}

	// Outcome does not dedupe a leader query that repeats IDs, so uniqueness is only asserted for unique queries.
	uniqueQuery := len(slices.Compact(slices.Sorted(slices.Values(q.RequestIDs)))) == len(q.RequestIDs)
	resolved := map[string]*types.RequestOutcome{}
	for _, ro := range outcome.Outcomes {
		require.True(t, slices.Contains(candidates, ro.RequestID), "outcome for request %q is neither queried nor recoverable", ro.RequestID)
		if uniqueQuery {
			require.NotContains(t, resolved, ro.RequestID, "duplicate outcome for request %q", ro.RequestID)
		}
		resolved[ro.RequestID] = ro
	}

	for _, id := range candidates {
		agreement, onlyEventuallyConsistent, mentions := map[string]int{}, true, 0
		for _, ob := range observations {
			requestObservation := ob.Observations[id]
			if requestObservation == nil {
				continue
			}
			mentions++
			if ec, ok := requestObservation.Observation.(*types.RequestObservation_EventuallyConsistent); ok {
				agreement[string(ec.EventuallyConsistent)]++
			} else {
				onlyEventuallyConsistent = false
			}
		}
		// Agreeing on a request needs a Byzantine quorum of observations that mention it, more than F of them identical.
		if onlyEventuallyConsistent && mentions >= byzQuorumSize(fuzzN, fuzzF) && slices.Max(slices.Collect(maps.Values(agreement))) > fuzzF {
			require.Contains(t, resolved, id, "request %q has a quorum of observations with more than F matching values and must be resolved", id)
		}
	}
	for id, ro := range resolved {
		if ec, ok := ro.Outcome.(*types.RequestOutcome_EventuallyConsistent); ok {
			backing := 0
			for _, ob := range observations {
				if requestObservation := ob.Observations[id]; requestObservation != nil {
					if observed, ok := requestObservation.Observation.(*types.RequestObservation_EventuallyConsistent); ok && slices.Equal(observed.EventuallyConsistent, ec.EventuallyConsistent) {
						backing++
					}
				}
			}
			require.Greater(t, backing, fuzzF, "agreed value for request %q must be backed by more than F observations", id)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
