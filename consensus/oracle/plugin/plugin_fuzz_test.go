package plugin_test

import (
	"encoding/hex"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/capabilities/consensus/metrics"
	"github.com/smartcontractkit/capabilities/consensus/oracle"
	"github.com/smartcontractkit/capabilities/consensus/oracle/plugin"
	oracletypes "github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	libocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	pbtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const (
	fuzzF           = 1
	fuzzN           = 4
	fuzzHonest      = fuzzN - 1
	fuzzBaseSeconds = int64(1_700_000_000)
)

type fuzzLimits struct {
	maxOutcomeBytes, maxRequestOutcomeBytes, maxReportBytes, maxReports uint32
}

var (
	bigFuzzLimits   = fuzzLimits{maxOutcomeBytes: 1_000_000, maxRequestOutcomeBytes: 1_000_000, maxReportBytes: 1_000_000, maxReports: 100}
	smallFuzzLimits = fuzzLimits{maxOutcomeBytes: 1300, maxRequestOutcomeBytes: 260, maxReportBytes: 230, maxReports: 2}
)

func newFuzzPlugin(t *testing.T, limits fuzzLimits, expirySpan uint64) ocr3types.ReportingPlugin[[]byte] {
	t.Helper()
	m, err := metrics.NewMetrics()
	require.NoError(t, err)
	p, err := plugin.NewReportingPlugin(logger.Nop(), m, fuzzF, fuzzN, requests.NewStore[*oracle.ConsensusRequest](), oracle.NewObservationQuorumTracker(), &pbtypes.ReportingPluginConfig{
		MaxQueryLengthBytes:              1_000_000,
		MaxObservationLengthBytes:        1_000_000,
		MaxOutcomeLengthBytes:            limits.maxOutcomeBytes,
		MaxReportLengthBytes:             limits.maxReportBytes,
		MaxReportCount:                   limits.maxReports,
		HistoricalOutcomeExpirySeqNrSpan: expirySpan,
	}, "evm", int(limits.maxRequestOutcomeBytes))
	require.NoError(t, err)
	return p
}

func fuzzMetadata(j int) oracle.ConsensusRequestMetadata {
	return oracle.ConsensusRequestMetadata{
		WorkflowID:               "0039525c34de895c8fa68006bd63f6ce4a45ef1bc66377e791c6a8ae803dc0e4",
		WorkflowOwner:            "1139525c34de895c8fa68006bd634387a9f1192a",
		WorkflowExecutionID:      hex.EncodeToString([]byte(fmt.Sprintf("%032d", j))),
		WorkflowName:             "a1b2c3d4e5f6a1b2c3d4",
		WorkflowDonID:            1,
		WorkflowDonConfigVersion: 1,
		ReferenceID:              fmt.Sprintf("%02x", j),
		KeyBundleID:              "evm",
		ReportID:                 "aabb",
	}
}

func fuzzDescriptor(kind uint8) *sdk.ConsensusDescriptor {
	aggregation := func(a sdk.AggregationType) *sdk.ConsensusDescriptor {
		return &sdk.ConsensusDescriptor{Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: a}}
	}
	switch kind % 3 {
	case 0:
		return aggregation(sdk.AggregationType_AGGREGATION_TYPE_MEDIAN)
	case 1:
		return aggregation(sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL)
	default:
		return &sdk.ConsensusDescriptor{Descriptor_: &sdk.ConsensusDescriptor_FieldsMap{FieldsMap: &sdk.FieldsMap{Fields: map[string]*sdk.ConsensusDescriptor{
			"a": aggregation(sdk.AggregationType_AGGREGATION_TYPE_MEDIAN),
			"b": aggregation(sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL),
		}}}}
	}
}

// fuzzValue builds the observed value. Only the map shape can be padded, which lets a case exceed the report and
// outcome size limits.
func fuzzValue(t testing.TB, kind uint8, v int64, padding int) *valuespb.Value {
	if kind%3 != 2 {
		return values.Proto(values.NewInt64(v))
	}
	m, err := values.NewMap(map[string]any{"a": v, "b": strings.Repeat("x", 1+4*padding)})
	require.NoError(t, err)
	return values.Proto(m)
}

// obsSpec describes what one oracle observes for every request in the round.
type obsSpec struct {
	kind        uint8
	value       int64
	isError     bool
	seconds     int64
	withDefault bool
	stricter    bool
	padding     int
}

func (s obsSpec) requestObservation(t testing.TB, md oracle.ConsensusRequestMetadata) *oracletypes.RequestObservation {
	input := &sdk.SimpleConsensusInputs{Descriptors: fuzzDescriptor(s.kind)}
	if s.withDefault && s.kind%3 != 2 {
		input.Default = values.Proto(values.NewInt64(7))
	}
	if s.isError {
		input.Observation = &sdk.SimpleConsensusInputs_Error{Error: "boom"}
	} else {
		input.Observation = &sdk.SimpleConsensusInputs_Value{Value: fuzzValue(t, s.kind, s.value, s.padding)}
	}
	return &oracletypes.RequestObservation{
		Metadata:                 plugin.ToRequestMetaData(md),
		Input:                    input,
		ReceivedAt:               &timestamppb.Timestamp{Seconds: s.seconds},
		Median_2Fplus1QuorumFlag: s.stricter,
	}
}

func marshalObservation(t testing.TB, nRequests int, s obsSpec) []byte {
	observations := map[string]*oracletypes.RequestObservation{}
	for j := range nRequests {
		md := fuzzMetadata(j)
		observations[md.RequestID()] = s.requestObservation(t, md)
	}
	b, err := proto.Marshal(&oracletypes.Observation{Observations: observations})
	require.NoError(t, err)
	return b
}

type fuzzCase struct {
	nRequests      int
	kind           uint8
	honest         []obsSpec
	limits         fuzzLimits
	expiry         uint64
	byzantine      []byte
	extraQueryIDs  []string
	withDefaultAny bool
	// errorObservations is how many of the observations for every request are errors that match the consensus metadata.
	errorObservations int
	// byzantineRaw is set when the Byzantine oracle sends arbitrary bytes instead of a well-formed observation.
	byzantineRaw bool
}

func sanitizeIDs(ids string) []string {
	var out []string
	for _, id := range strings.Split(strings.ToValidUTF8(ids, "?"), ",") {
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func nonExpiredHistory(t *testing.T, prev []byte, seqNr, span uint64) map[string]uint64 {
	kept := map[string]uint64{}
	if prev == nil {
		return kept
	}
	previous := &oracletypes.Outcome{}
	require.NoError(t, proto.Unmarshal(prev, previous))
	for id, outcomeSeqNr := range previous.HistoricalOutcomes {
		if seqNr-outcomeSeqNr <= span {
			kept[id] = outcomeSeqNr
		}
	}
	return kept
}

func asInt64(t *testing.T, unwrapped any) int64 {
	t.Helper()
	switch v := unwrapped.(type) {
	case int64:
		return v
	case interface{ IntPart() int64 }:
		return v.IntPart()
	default:
		require.Failf(t, "unexpected value type", "%T", unwrapped)
		return 0
	}
}

// requireMalformedObservationsRejected checks that observations a Byzantine oracle could send to confuse the plugin
// never pass validation.
func requireMalformedObservationsRejected(t *testing.T, p ocr3types.ReportingPlugin[[]byte], outctx ocr3types.OutcomeContext, query []byte, queried []string) {
	t.Helper()
	md0, md7, md9 := fuzzMetadata(0), fuzzMetadata(7), fuzzMetadata(9)
	good := obsSpec{value: 1, seconds: fuzzBaseSeconds}
	observation := func(key string, ro *oracletypes.RequestObservation) []byte {
		b, err := proto.Marshal(&oracletypes.Observation{Observations: map[string]*oracletypes.RequestObservation{key: ro}})
		require.NoError(t, err)
		return b
	}

	withoutMetadata := good.requestObservation(t, md0)
	withoutMetadata.Metadata = nil
	withoutInput := good.requestObservation(t, md0)
	withoutInput.Input = nil
	malformed := map[string][]byte{
		"key does not match metadata request ID": observation(md0.RequestID(), good.requestObservation(t, md7)),
		"missing metadata":                       observation(md0.RequestID(), withoutMetadata),
		"missing input":                          observation(md0.RequestID(), withoutInput),
	}
	if !slices.Contains(queried, md9.RequestID()) {
		malformed["request not in the query"] = observation(md9.RequestID(), good.requestObservation(t, md9))
	}
	for name, raw := range malformed {
		err := p.ValidateObservation(t.Context(), outctx, query, libocrtypes.AttributedObservation{Observation: raw})
		require.Error(t, err, "observation with %s must be rejected", name)
	}
}

// runFuzzRound executes one OCR round and returns the outcome, or nil when the observation quorum was not reached.
func runFuzzRound(t *testing.T, c fuzzCase, seqNr uint64, prev []byte) []byte {
	t.Helper()
	ctx := t.Context()

	requestIDs := make([]string, 0, c.nRequests+len(c.extraQueryIDs)+1)
	for j := range c.nRequests {
		requestIDs = append(requestIDs, fuzzMetadata(j).RequestID())
	}
	// A repeated ID and unknown IDs both come from an untrusted leader's query.
	queried := append(slices.Clone(requestIDs), requestIDs[0])
	queried = append(queried, c.extraQueryIDs...)
	query, err := proto.Marshal(&oracletypes.Query{RequestIDs: queried})
	require.NoError(t, err)
	uniqueQueried := slices.Compact(slices.Sorted(slices.Values(queried)))

	outctx := ocr3types.OutcomeContext{SeqNr: seqNr, PreviousOutcome: prev}

	var aos []libocrtypes.AttributedObservation
	for i, spec := range c.honest {
		aos = append(aos, libocrtypes.AttributedObservation{Observer: commontypes.OracleID(i), Observation: marshalObservation(t, c.nRequests, spec)})
	}
	aos = append(aos, libocrtypes.AttributedObservation{Observer: fuzzHonest, Observation: c.byzantine})

	validator := newFuzzPlugin(t, c.limits, c.expiry)
	requireMalformedObservationsRejected(t, validator, outctx, query, uniqueQueried)
	var validated []libocrtypes.AttributedObservation
	for i, ao := range aos {
		err := validator.ValidateObservation(ctx, outctx, query, ao)
		if i < len(c.honest) {
			require.NoError(t, err, "an honest observation must validate")
		}
		if err == nil {
			validated = append(validated, ao)
		}
	}

	quorum, err := validator.ObservationQuorum(ctx, outctx, query, validated)
	require.NoError(t, err)
	if !quorum {
		return nil
	}

	// Outcome sorts the slice it is given, so every call gets its own copy.
	outcome := func(in []libocrtypes.AttributedObservation) []byte {
		out, err := newFuzzPlugin(t, c.limits, c.expiry).Outcome(ctx, outctx, query, slices.Clone(in))
		require.NoError(t, err, "Outcome must succeed for validated observations and a well-formed previous outcome")
		return out
	}
	first := outcome(validated)
	require.Equal(t, first, outcome(validated), "same inputs must yield identical outcome")
	reversed := slices.Clone(validated)
	slices.Reverse(reversed)
	require.Equal(t, first, outcome(reversed), "outcome must not depend on observation order")

	requireOutcomeInvariants(t, c, first, prev, seqNr, uniqueQueried, validated)
	requireReportInvariants(t, c, seqNr, first)
	return first
}

func requireOutcomeInvariants(t *testing.T, c fuzzCase, raw, prev []byte, seqNr uint64, queried []string, validated []libocrtypes.AttributedObservation) {
	t.Helper()
	outcome := &oracletypes.Outcome{}
	require.NoError(t, proto.Unmarshal(raw, outcome))

	observationsByRequest := map[string]int{}
	for _, ao := range validated {
		obs := &oracletypes.Observation{}
		require.NoError(t, proto.Unmarshal(ao.Observation, obs))
		for id := range obs.Observations {
			observationsByRequest[id]++
		}
	}

	previousHistory := nonExpiredHistory(t, prev, seqNr, c.expiry)
	outcomeIDs := map[string]*oracletypes.ConsensusOutcome{}
	for _, ro := range outcome.Outcomes {
		var id string
		switch o := ro.GetOutcome().(type) {
		case *oracletypes.ConsensusOutcome_Success:
			id = o.Success.Metadata.RequestId
		case *oracletypes.ConsensusOutcome_Failure:
			id = o.Failure.RequestID
		default:
			require.Fail(t, "outcome of unknown type")
		}
		require.Contains(t, queried, id, "outcome for a request that was not queried")
		require.NotContains(t, outcomeIDs, id, "duplicate outcome for request %q", id)
		require.NotContains(t, previousHistory, id, "request %q already has a historical outcome", id)
		require.GreaterOrEqual(t, observationsByRequest[id], 2*fuzzF+1, "request %q resolved without a 2F+1 quorum of observations", id)
		outcomeIDs[id] = ro
	}

	wantHistory := map[string]uint64{}
	for id, s := range previousHistory {
		wantHistory[id] = s
	}
	for id := range outcomeIDs {
		wantHistory[id] = seqNr
	}
	require.True(t, maps.Equal(wantHistory, outcome.HistoricalOutcomes), "historical outcomes must be the unexpired previous ones plus this round's: want %v, got %v", wantHistory, outcome.HistoricalOutcomes)

	if len(outcome.Outcomes) > 0 {
		require.LessOrEqual(t, len(raw), int(c.limits.maxOutcomeBytes), "outcome must respect MaxOutcomeLengthBytes")
	}
	require.LessOrEqual(t, len(outcome.Outcomes), int(c.limits.maxReports), "outcome must not hold more requests than reports")

	if c.limits == bigFuzzLimits {
		for _, id := range queried {
			if _, done := previousHistory[id]; done || observationsByRequest[id] < 2*fuzzF+1 {
				continue
			}
			require.Contains(t, outcomeIDs, id, "request %q has a quorum of observations and must be resolved", id)
		}
		requireErrorQuorumInvariant(t, c, outcomeIDs)
		requireValueInvariants(t, c, outcomeIDs)
	}
}

// requireErrorQuorumInvariant checks that more than F matching error observations can only end in a failed consensus,
// or in the request's default value.
func requireErrorQuorumInvariant(t *testing.T, c fuzzCase, outcomes map[string]*oracletypes.ConsensusOutcome) {
	t.Helper()
	if c.errorObservations <= fuzzF {
		// A single Byzantine error must not fail a request. Raw Byzantine bytes may themselves be a matching error,
		// so the count is only a lower bound there.
		if !c.byzantineRaw {
			for id, ro := range outcomes {
				if failure := ro.GetFailure(); failure != nil {
					require.NotEqual(t, oracletypes.ConsensusFailureCode_RECEIVED_FPLUS1_ERRORS, failure.Code, "request %q failed on F or fewer errors", id)
				}
			}
		}
		return
	}
	for id, ro := range outcomes {
		if failure := ro.GetFailure(); failure != nil {
			require.Equal(t, oracletypes.ConsensusFailureCode_RECEIVED_FPLUS1_ERRORS, failure.Code, "request %q with more than F errors must fail for that reason", id)
			continue
		}
		require.True(t, c.withDefaultAny && c.kind%3 != 2, "request %q with more than F errors may only succeed with its default value", id)
		value := &valuespb.Value{}
		require.NoError(t, proto.Unmarshal(ro.GetSuccess().Outcome, value))
		unwrapped, err := values.FromProto(value)
		require.NoError(t, err)
		raw, err := unwrapped.Unwrap()
		require.NoError(t, err)
		require.Equal(t, int64(7), asInt64(t, raw), "request %q with more than F errors must use its default value", id)
	}
}

// requireValueInvariants checks that one Byzantine oracle cannot move an aggregated value or timestamp outside the
// range the honest oracles reported. That only holds when more than F honest oracles contributed a value: with a single
// honest value (the others reported errors) the median and timestamp are taken over two values, one of them Byzantine.
func requireValueInvariants(t *testing.T, c fuzzCase, outcomes map[string]*oracletypes.ConsensusOutcome) {
	t.Helper()
	var honestValues []int64
	lowSeconds, highSeconds := int64(math.MaxInt64), int64(math.MinInt64)
	for _, h := range c.honest {
		if h.isError {
			continue
		}
		lowSeconds, highSeconds = min(lowSeconds, h.seconds), max(highSeconds, h.seconds)
		honestValues = append(honestValues, h.value)
	}
	if len(honestValues) <= fuzzF {
		return
	}

	for id, ro := range outcomes {
		success := ro.GetSuccess()
		if success == nil {
			continue
		}
		require.GreaterOrEqual(t, success.Timestamp.Seconds, lowSeconds, "timestamp of %q below the honest range", id)
		require.LessOrEqual(t, success.Timestamp.Seconds, highSeconds, "timestamp of %q above the honest range", id)

		if c.withDefaultAny || c.kind%3 == 2 {
			continue
		}
		value := &valuespb.Value{}
		require.NoError(t, proto.Unmarshal(success.Outcome, value))
		unwrapped, err := values.FromProto(value)
		require.NoError(t, err)
		raw, err := unwrapped.Unwrap()
		require.NoError(t, err)
		got := asInt64(t, raw)
		switch c.kind % 3 {
		case 0:
			require.GreaterOrEqual(t, got, slices.Min(honestValues), "median of %q below the honest range", id)
			require.LessOrEqual(t, got, slices.Max(honestValues), "median of %q above the honest range", id)
		case 1:
			require.Contains(t, honestValues, got, "identical consensus on %q must be a value an honest oracle reported", id)
		}
	}
}

func requireReportInvariants(t *testing.T, c fuzzCase, seqNr uint64, rawOutcome []byte) {
	t.Helper()
	outcome := &oracletypes.Outcome{}
	require.NoError(t, proto.Unmarshal(rawOutcome, outcome))

	p := newFuzzPlugin(t, c.limits, c.expiry)
	reports, err := p.Reports(t.Context(), seqNr, rawOutcome)
	require.NoError(t, err, "Reports must handle every outcome the plugin produced")
	again, err := newFuzzPlugin(t, c.limits, c.expiry).Reports(t.Context(), seqNr, rawOutcome)
	require.NoError(t, err)
	require.Equal(t, reports, again, "same outcome must yield identical reports")

	require.Len(t, reports, len(outcome.Outcomes), "every outcome yields exactly one report")
	require.LessOrEqual(t, len(reports), int(c.limits.maxReports))
	for i, report := range reports {
		info := &structpb.Struct{}
		require.NoError(t, proto.Unmarshal(report.ReportWithInfo.Info, info))
		var wantID string
		switch o := outcome.Outcomes[i].GetOutcome().(type) {
		case *oracletypes.ConsensusOutcome_Success:
			wantID = o.Success.Metadata.RequestId
		case *oracletypes.ConsensusOutcome_Failure:
			wantID = o.Failure.RequestID
		}
		require.Equal(t, wantID, info.Fields[plugin.InfoRequestID].GetStringValue(), "report %d must be for outcome %d", i, i)
		if len(report.ReportWithInfo.Report) > 0 {
			require.GreaterOrEqual(t, len(report.ReportWithInfo.Report), plugin.ReportMetaDataPrependLength)
			require.LessOrEqual(t, len(report.ReportWithInfo.Report), int(c.limits.maxReportBytes), "report must respect MaxReportLengthBytes")
		}
	}
}

func FuzzReportingPlugin_OutcomeAcrossRounds(f *testing.F) {
	validObservation := func(spec obsSpec, n int) []byte {
		return marshalObservation(f, n, spec)
	}
	// seqNr, stricterMedian, padding, gap, expiry, nRequests, kind, v0..v2, errMask, s0..s2, withDefault, small, drop, byzMode, byzValue, byzSeconds, extraIDs, byzRaw
	f.Add(uint64(10), false, uint8(0), uint8(0), uint8(5), uint8(0), uint8(0), int32(10), int32(20), int32(30), uint8(0), int32(0), int32(5), int32(9), false, false, false, uint8(1), int32(1_000_000), int64(0), "", []byte{})
	f.Add(uint64(10), false, uint8(0), uint8(0), uint8(5), uint8(1), uint8(1), int32(7), int32(7), int32(8), uint8(0), int32(0), int32(0), int32(0), false, false, false, uint8(1), int32(99), int64(0), "unknown-1", []byte{})
	f.Add(uint64(10), false, uint8(0), uint8(3), uint8(2), uint8(2), uint8(2), int32(1), int32(2), int32(3), uint8(0b011), int32(0), int32(1), int32(2), true, false, false, uint8(3), int32(0), int64(0), "", []byte{})
	f.Add(uint64(10), false, uint8(0), uint8(0), uint8(5), uint8(2), uint8(0), int32(1), int32(2), int32(3), uint8(0), int32(0), int32(1), int32(2), false, true, false, uint8(4), int32(0), int64(math.MaxInt64), "", []byte{})
	f.Add(uint64(10), false, uint8(0), uint8(0), uint8(5), uint8(0), uint8(0), int32(1), int32(2), int32(3), uint8(0), int32(5), int32(1), int32(2), false, false, true, uint8(4), int32(0), int64(math.MinInt64), "", validObservation(obsSpec{value: 2, seconds: fuzzBaseSeconds}, 1))
	f.Add(uint64(10), false, uint8(0), uint8(0), uint8(5), uint8(1), uint8(0), int32(1), int32(2), int32(3), uint8(0), int32(0), int32(1), int32(2), false, false, false, uint8(2), int32(0), int64(0), "", []byte{})
	f.Add(uint64(10), false, uint8(0), uint8(0), uint8(5), uint8(1), uint8(1), int32(1), int32(1), int32(1), uint8(0), int32(0), int32(1), int32(2), true, false, false, uint8(5), int32(0), int64(0), "", []byte{0xff})
	f.Add(uint64(10), false, uint8(0), uint8(0), uint8(5), uint8(0), uint8(0), int32(1), int32(2), int32(3), uint8(0), int32(0), int32(1), int32(2), false, false, false, uint8(0), int32(0), int64(0), "", []byte{0x0a, 0x00})
	// only two honest oracles plus an empty observation: the observation quorum holds but no request has 2F+1 observations
	f.Add(uint64(10), false, uint8(0), uint8(0), uint8(5), uint8(1), uint8(0), int32(1), int32(2), int32(3), uint8(0), int32(0), int32(1), int32(2), false, false, true, uint8(0), int32(0), int64(0), "", []byte{})
	// the second round lands exactly on, and just past, the historical outcome expiry
	f.Add(uint64(10), false, uint8(0), uint8(1), uint8(2), uint8(1), uint8(0), int32(1), int32(2), int32(3), uint8(0), int32(0), int32(1), int32(2), false, false, false, uint8(1), int32(5), int64(0), "", []byte{})
	f.Add(uint64(10), false, uint8(0), uint8(2), uint8(2), uint8(1), uint8(0), int32(1), int32(2), int32(3), uint8(0), int32(0), int32(1), int32(2), false, false, false, uint8(1), int32(5), int64(0), "", []byte{})
	// large values against small limits: an oversized report, and an outcome that cannot fit even an empty batch
	f.Add(uint64(10), false, uint8(60), uint8(0), uint8(5), uint8(2), uint8(2), int32(1), int32(2), int32(3), uint8(0), int32(0), int32(1), int32(2), false, true, false, uint8(1), int32(5), int64(0), "", []byte{})
	f.Add(uint64(10), false, uint8(255), uint8(0), uint8(5), uint8(2), uint8(2), int32(1), int32(2), int32(3), uint8(0), int32(0), int32(1), int32(2), false, true, false, uint8(1), int32(5), int64(0), "", []byte{})
	// exactly F+1 errors (one honest, one Byzantine) while two honest values are present
	f.Add(uint64(10), false, uint8(0), uint8(0), uint8(5), uint8(0), uint8(0), int32(10), int32(20), int32(30), uint8(0b001), int32(0), int32(1), int32(2), false, false, false, uint8(3), int32(0), int64(0), "", []byte{})
	f.Add(uint64(10), false, uint8(0), uint8(0), uint8(5), uint8(0), uint8(1), int32(10), int32(20), int32(30), uint8(0b001), int32(0), int32(1), int32(2), true, false, false, uint8(3), int32(0), int64(0), "", []byte{})
	// stricter median quorum, with and without a second honest value
	f.Add(uint64(10), true, uint8(0), uint8(0), uint8(5), uint8(0), uint8(0), int32(10), int32(20), int32(30), uint8(0), int32(0), int32(5), int32(9), false, false, false, uint8(1), int32(1_000_000), int64(0), "", []byte{})
	f.Add(uint64(10), true, uint8(0), uint8(0), uint8(5), uint8(0), uint8(0), int32(10), int32(20), int32(30), uint8(0b100), int32(0), int32(5), int32(9), false, false, false, uint8(4), int32(0), int64(math.MaxInt64), "", []byte{})

	f.Fuzz(func(t *testing.T, seqNr uint64, stricterMedian bool, padding, gap, expiry, nRequests, kind uint8, v0, v1, v2 int32, errMask uint8, s0, s1, s2 int32,
		withDefault, small, drop bool, byzMode uint8, byzValue int32, byzSeconds int64, extraIDs string, byzRaw []byte) {
		seqNr %= 1 << 62

		c := fuzzCase{
			nRequests:      1 + int(nRequests%3),
			kind:           kind,
			limits:         bigFuzzLimits,
			expiry:         uint64(expiry),
			extraQueryIDs:  sanitizeIDs(extraIDs),
			withDefaultAny: withDefault,
		}
		if small {
			c.limits = smallFuzzLimits
		}
		secs := [fuzzHonest]int64{fuzzBaseSeconds + int64(s0), fuzzBaseSeconds + int64(s1), fuzzBaseSeconds + int64(s2)}
		vals := [fuzzHonest]int64{int64(v0), int64(v1), int64(v2)}
		honestCount := fuzzHonest
		if drop {
			honestCount--
		}
		for i := range honestCount {
			c.honest = append(c.honest, obsSpec{kind: kind, value: vals[i], isError: errMask>>i&1 == 1, seconds: secs[i], withDefault: withDefault, stricter: stricterMedian, padding: int(padding)})
		}

		for _, h := range c.honest {
			if h.isError {
				c.errorObservations++
			}
		}
		if byzMode%6 == 3 {
			c.errorObservations++
		}
		c.byzantineRaw = byzMode%6 == 0

		byz := obsSpec{kind: kind, value: int64(byzValue), seconds: secs[0], withDefault: withDefault, stricter: stricterMedian, padding: int(padding)}
		switch byzMode % 6 {
		case 0:
			c.byzantine = byzRaw
		case 1:
		case 2:
			byz.kind = kind + 1
		case 3:
			byz.isError = true
		case 4:
			byz.seconds = byzSeconds
		case 5:
			byz.withDefault = !withDefault
		}
		if byzMode%6 != 0 {
			c.byzantine = marshalObservation(t, c.nRequests, byz)
		}

		first := runFuzzRound(t, c, seqNr, nil)
		if first == nil {
			return
		}
		runFuzzRound(t, c, seqNr+uint64(gap)+1, first)
	})
}
