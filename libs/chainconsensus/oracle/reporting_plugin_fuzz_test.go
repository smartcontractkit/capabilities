package oracle

import (
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
		if len(aos) == 0 {
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
		// Outcome does not dedupe a leader query that repeats IDs, so uniqueness is only asserted for unique queries.
		uniqueQuery := len(slices.Compact(slices.Sorted(slices.Values(q.RequestIDs)))) == len(q.RequestIDs)
		seen := map[string]bool{}
		for _, ro := range outcome.Outcomes {
			require.True(t, slices.Contains(q.RequestIDs, ro.RequestID), "outcome for request %q not in query", ro.RequestID)
			if uniqueQuery {
				require.False(t, seen[ro.RequestID], "duplicate outcome for request %q", ro.RequestID)
			}
			seen[ro.RequestID] = true
		}

		reports, err := newFuzzPlugin(t).Reports(t.Context(), 1, first)
		require.NoError(t, err)
		require.LessOrEqual(t, len(reports), fuzzMaxReportCount)
		for _, r := range reports {
			require.LessOrEqual(t, len(r.ReportWithInfo.Report), fuzzMaxReportLenBytes)
		}
		again, err := newFuzzPlugin(t).Reports(t.Context(), 1, first)
		require.NoError(t, err)
		require.Equal(t, reports, again, "same outcome must yield identical reports")
	})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
