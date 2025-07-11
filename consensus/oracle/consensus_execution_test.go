package oracle

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/values"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"
)

func Test_CalculateOutcomeForObservations(t *testing.T) {
	type testCase struct {
		name            string
		observations    []*valuespb.Value
		descriptor      *pb.ConsensusDescriptor
		minObs          int
		expectedOutcome *valuespb.Value
		expectedError   error
	}

	testCases := []testCase{
		{
			name: "insufficient observations (initial check)",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
			},
			descriptor: &pb.ConsensusDescriptor{
				Descriptor_: &pb.ConsensusDescriptor_Aggregation{
					Aggregation: pb.AggregationType_AGGREGATION_TYPE_MEDIAN,
				},
			},
			minObs:          3,
			expectedOutcome: nil,
			expectedError:   errors.New("insufficient observations"),
		},
		{
			name: "median aggregation: happy path (int64)",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(30)),
				values.Proto(values.NewInt64(40)),
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(50)),
			},
			descriptor: &pb.ConsensusDescriptor{
				Descriptor_: &pb.ConsensusDescriptor_Aggregation{
					Aggregation: pb.AggregationType_AGGREGATION_TYPE_MEDIAN,
				},
			},
			minObs:          5,
			expectedOutcome: values.Proto(values.NewInt64(30)),
			expectedError:   nil,
		},
		{
			name: "median: mixed types, one dominant (int64) - handled by filtering",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)), values.Proto(values.NewFloat64(1.0)),
				values.Proto(values.NewInt64(20)), values.Proto(values.NewFloat64(2.0)),
				values.Proto(values.NewInt64(30)), values.Proto(values.NewInt64(40)),
			},
			descriptor: &pb.ConsensusDescriptor{
				Descriptor_: &pb.ConsensusDescriptor_Aggregation{
					Aggregation: pb.AggregationType_AGGREGATION_TYPE_MEDIAN,
				},
			},
			minObs:          4,
			expectedOutcome: values.Proto(values.NewInt64(20)),
			expectedError:   nil,
		},
		{
			name: "common prefix aggregation: not yet supported",
			observations: []*valuespb.Value{
				values.Proto(values.NewString("abc")),
			},
			descriptor: &pb.ConsensusDescriptor{
				Descriptor_: &pb.ConsensusDescriptor_Aggregation{
					Aggregation: pb.AggregationType_AGGREGATION_TYPE_COMMON_PREFIX,
				},
			},
			minObs:          1,
			expectedOutcome: nil,
			expectedError:   errors.New("common prefix aggregation type not supported"),
		},
		{
			name: "common suffix aggregation: not yet supported",
			observations: []*valuespb.Value{
				values.Proto(values.NewString("xyz")),
			},
			descriptor: &pb.ConsensusDescriptor{
				Descriptor_: &pb.ConsensusDescriptor_Aggregation{
					Aggregation: pb.AggregationType_AGGREGATION_TYPE_COMMON_SUFFIX,
				},
			},
			minObs:          1,
			expectedOutcome: nil,
			expectedError:   errors.New("common suffix aggregation type not supported"),
		},
		{
			name: "unknown aggregation type (UNSPECIFIED)",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
			},
			descriptor: &pb.ConsensusDescriptor{
				Descriptor_: &pb.ConsensusDescriptor_Aggregation{
					Aggregation: pb.AggregationType_AGGREGATION_TYPE_UNSPECIFIED,
				},
			},
			minObs:          1,
			expectedOutcome: nil,
			expectedError:   errors.New("unknown aggregation type"),
		},
		{
			name: "unsupported consensus descriptor type (FieldsMap)",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
			},
			descriptor: &pb.ConsensusDescriptor{
				Descriptor_: &pb.ConsensusDescriptor_FieldsMap{
					FieldsMap: &pb.FieldsMap{},
				},
			},
			minObs:          1,
			expectedOutcome: nil,
			expectedError:   errors.New("TODO only primitive aggregation types are supported right now"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			oh := newObservationHandler()
			outcome, err := oh.CalculateOutcomeForObservations(
				tc.observations,
				tc.descriptor,
				tc.minObs,
			)

			if tc.expectedError != nil {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedError.Error(), "Error message mismatch")
			} else {
				require.NoError(t, err, "Unexpected error for test case %s", tc.name)
			}

			if tc.expectedError == nil {
				require.True(t, proto.Equal(outcome, tc.expectedOutcome),
					"Outcome mismatch for %s\nExpected: %+v\nActual:   %+v", tc.name, tc.expectedOutcome, outcome)
			}
		})
	}
}

// Test_handleMedianAggregation tests the handleMedianAggregation function directly.
func Test_handleMedianAggregation(t *testing.T) {
	type testCase struct {
		name              string
		observations      []values.Value
		finalSelectedType string
		expectedOutcome   *valuespb.Value
		expectedError     error
	}

	testCases := []testCase{
		{
			name: "int64 median: basic five values",
			observations: []values.Value{
				values.NewInt64(30), values.NewInt64(40), values.NewInt64(10), values.NewInt64(20), values.NewInt64(50),
			},
			finalSelectedType: TypeInt64,
			expectedOutcome:   values.Proto(values.NewInt64(30)),
			expectedError:     nil,
		},
		{
			name: "int64 median: even number of values returns left value",
			observations: []values.Value{
				values.NewInt64(10), values.NewInt64(20), values.NewInt64(30), values.NewInt64(40),
			},
			finalSelectedType: TypeInt64,
			expectedOutcome:   values.Proto(values.NewInt64(20)),
			expectedError:     nil,
		},
		{
			name: "float64 median: basic five values",
			observations: []values.Value{
				values.NewFloat64(30.5), values.NewFloat64(40.5), values.NewFloat64(10.5), values.NewFloat64(20.5), values.NewFloat64(50.5),
			},
			finalSelectedType: TypeFloat64,
			expectedOutcome:   values.Proto(values.NewFloat64(30.5)),
			expectedError:     nil,
		},
		{
			name: "float64 median: even number of values returns left value",
			observations: []values.Value{
				values.NewFloat64(10.5), values.NewFloat64(20.5), values.NewFloat64(30.5), values.NewFloat64(40.5),
			},
			finalSelectedType: TypeFloat64,
			expectedOutcome:   values.Proto(values.NewFloat64(20.5)),
			expectedError:     nil,
		},
		{
			name: "decimal median: basic five values",
			observations: []values.Value{
				values.NewDecimal(decimal.NewFromFloat(30.3)), values.NewDecimal(decimal.NewFromFloat(40.4)),
				values.NewDecimal(decimal.NewFromFloat(10.1)), values.NewDecimal(decimal.NewFromFloat(20.2)),
				values.NewDecimal(decimal.NewFromFloat(50.5)),
			},
			finalSelectedType: TypeDecimal,
			expectedOutcome:   values.Proto(values.NewDecimal(decimal.NewFromFloat(30.3))),
			expectedError:     nil,
		},
		{
			name: "decimal median: even number of values returns left value",
			observations: []values.Value{
				values.NewDecimal(decimal.NewFromFloat(10.1)), values.NewDecimal(decimal.NewFromFloat(20.2)),
				values.NewDecimal(decimal.NewFromFloat(30.3)), values.NewDecimal(decimal.NewFromFloat(40.4)),
			},
			finalSelectedType: TypeDecimal,
			expectedOutcome:   values.Proto(values.NewDecimal(decimal.NewFromFloat(20.2))),
			expectedError:     nil,
		},
		{
			name: "bigint median: basic five values",
			observations: []values.Value{
				values.NewBigInt(big.NewInt(300)), values.NewBigInt(big.NewInt(400)),
				values.NewBigInt(big.NewInt(100)), values.NewBigInt(big.NewInt(200)),
				values.NewBigInt(big.NewInt(500)),
			},
			finalSelectedType: TypeBigInt,
			expectedOutcome:   values.Proto(values.NewBigInt(big.NewInt(300))),
			expectedError:     nil,
		},
		{
			name: "bigint median: even number of values returns left value",
			observations: []values.Value{
				values.NewBigInt(big.NewInt(100)), values.NewBigInt(big.NewInt(200)),
				values.NewBigInt(big.NewInt(300)), values.NewBigInt(big.NewInt(400)),
			},
			finalSelectedType: TypeBigInt,
			expectedOutcome:   values.Proto(values.NewBigInt(big.NewInt(200))),
			expectedError:     nil,
		},
		{
			name: "time median: basic five values",
			observations: []values.Value{
				values.NewTime(parseTime(t, "2023-01-01T00:00:30Z")),
				values.NewTime(parseTime(t, "2023-01-01T00:00:40Z")),
				values.NewTime(parseTime(t, "2023-01-01T00:00:10Z")),
				values.NewTime(parseTime(t, "2023-01-01T00:00:20Z")),
				values.NewTime(parseTime(t, "2023-01-01T00:00:50Z")),
			},
			finalSelectedType: TypeTime,
			expectedOutcome:   values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:30Z"))),
			expectedError:     nil,
		},
		{
			name: "time median: even number of values returns left value",
			observations: []values.Value{
				values.NewTime(parseTime(t, "2023-01-01T00:00:10Z")),
				values.NewTime(parseTime(t, "2023-01-01T00:00:20Z")),
				values.NewTime(parseTime(t, "2023-01-01T00:00:30Z")),
				values.NewTime(parseTime(t, "2023-01-01T00:00:40Z")),
			},
			finalSelectedType: TypeTime,
			expectedOutcome:   values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:20Z"))),
			expectedError:     nil,
		},
		{
			name: "median: unsupported type for median aggregation (string)",
			observations: []values.Value{
				values.NewString("foo"), values.NewString("bar"), values.NewString("baz"),
			},
			finalSelectedType: TypeString,
			expectedOutcome:   nil,
			expectedError:     errors.New("unsupported type for median aggregation: " + TypeString),
		},
		{
			name:              "empty filtered observations for median",
			observations:      []values.Value{},
			finalSelectedType: TypeFloat64, // A type not present in observations
			expectedOutcome:   nil,
			expectedError:     errors.New("no valid observations for median calculation"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			oh := newObservationHandler()
			outcome, err := oh.handleMedianAggregation(
				tc.observations,
				tc.finalSelectedType,
			)

			if tc.expectedError != nil {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedError.Error(), "Error message mismatch")
			} else {
				require.NoError(t, err, "Unexpected error for test case %s", tc.name)
			}

			if tc.expectedError == nil {
				require.True(t, proto.Equal(outcome, tc.expectedOutcome),
					"Outcome mismatch for %s\nExpected: %+v\nActual:   %+v", tc.name, tc.expectedOutcome, outcome)
			}
		})
	}
}

// Test_handleIdenticalAggregation tests the handleIdenticalAggregation function directly.
func Test_handleIdenticalAggregation(t *testing.T) {
	type testCase struct {
		name              string
		observations      []values.Value
		finalSelectedType string
		expectedOutcome   *valuespb.Value
		expectedError     error
	}

	testCases := []testCase{
		{
			name: "int64 identical: all identical",
			observations: []values.Value{
				values.NewInt64(10), values.NewInt64(10), values.NewInt64(10),
			},
			finalSelectedType: TypeInt64,
			expectedOutcome:   values.Proto(values.NewInt64(10)),
			expectedError:     nil,
		},
		{
			name: "int64 identical: not all identical",
			observations: []values.Value{
				values.NewInt64(10), values.NewInt64(20), values.NewInt64(10),
			},
			finalSelectedType: TypeInt64,
			expectedOutcome:   nil,
			expectedError:     errors.New("observations are not identical: mismatch found at index 1"),
		},
		{
			name:              "int64 identical: empty observations",
			observations:      []values.Value{},
			finalSelectedType: TypeInt64,
			expectedOutcome:   nil,
			expectedError:     errors.New("no observations to determine identical value"),
		},
		{
			name: "string identical: all identical",
			observations: []values.Value{
				values.NewString("hello"), values.NewString("hello"),
			},
			finalSelectedType: TypeString,
			expectedOutcome:   values.Proto(values.NewString("hello")),
			expectedError:     nil,
		},
		{
			name: "string identical: not all identical",
			observations: []values.Value{
				values.NewString("hello"), values.NewString("world"),
			},
			finalSelectedType: TypeString,
			expectedOutcome:   nil,
			expectedError:     errors.New("observations are not identical: mismatch found at index 1"),
		},
		{
			name: "bool identical: all identical (true)",
			observations: []values.Value{
				values.NewBool(true), values.NewBool(true),
			},
			finalSelectedType: TypeBool,
			expectedOutcome:   values.Proto(values.NewBool(true)),
			expectedError:     nil,
		},
		{
			name: "bytes identical: all identical",
			observations: []values.Value{
				values.NewBytes([]byte("data")), values.NewBytes([]byte("data")),
			},
			finalSelectedType: TypeBytes,
			expectedOutcome:   values.Proto(values.NewBytes([]byte("data"))),
			expectedError:     nil,
		},
		{
			name: "time identical: all identical",
			observations: []values.Value{
				values.NewTime(parseTime(t, "2023-01-01T10:00:00Z")),
				values.NewTime(parseTime(t, "2023-01-01T10:00:00Z")),
			},
			finalSelectedType: TypeTime,
			expectedOutcome:   values.Proto(values.NewTime(parseTime(t, "2023-01-01T10:00:00Z"))),
			expectedError:     nil,
		},
		{
			name: "map identical: simple maps, all identical",
			observations: []values.Value{
				mustNewMap(t, map[string]any{"a": 1, "b": "x"}),
				mustNewMap(t, map[string]any{"a": 1, "b": "x"}),
			},
			finalSelectedType: TypeMap,
			expectedOutcome:   values.Proto(mustNewMap(t, map[string]any{"a": 1, "b": "x"})),
			expectedError:     nil,
		},
		{
			name: "map identical: nested maps, all identical",
			observations: []values.Value{
				mustNewMap(t, map[string]any{"a": 1, "b": map[string]any{"c": "d"}}),
				mustNewMap(t, map[string]any{"a": 1, "b": map[string]any{"c": "d"}}),
			},
			finalSelectedType: TypeMap,
			expectedOutcome:   values.Proto(mustNewMap(t, map[string]any{"a": 1, "b": map[string]any{"c": "d"}})),
			expectedError:     nil,
		},
		{
			name: "map identical: maps with lists, all identical",
			observations: []values.Value{
				mustNewMap(t, map[string]any{"data": []any{1, "two", true}}),
				mustNewMap(t, map[string]any{"data": []any{1, "two", true}}),
			},
			finalSelectedType: TypeMap,
			expectedOutcome:   values.Proto(mustNewMap(t, map[string]any{"data": []any{1, "two", true}})),
			expectedError:     nil,
		},
		{
			name: "map identical: maps with nil values, all identical",
			observations: []values.Value{
				mustNewMap(t, map[string]any{"a": 1, "b": nil}),
				mustNewMap(t, map[string]any{"a": 1, "b": nil}),
			},
			finalSelectedType: TypeMap,
			expectedOutcome:   values.Proto(mustNewMap(t, map[string]any{"a": 1, "b": nil})),
			expectedError:     nil,
		},
		{
			name: "map identical: not all identical (different value)",
			observations: []values.Value{
				mustNewMap(t, map[string]any{"a": 1}),
				mustNewMap(t, map[string]any{"a": 2}),
			},
			finalSelectedType: TypeMap,
			expectedOutcome:   nil,
			expectedError:     errors.New("observations are not identical: mismatch found at index 1"),
		},
		{
			name: "map identical: not all identical (different key)",
			observations: []values.Value{
				mustNewMap(t, map[string]any{"a": 1}),
				mustNewMap(t, map[string]any{"b": 1}),
			},
			finalSelectedType: TypeMap,
			expectedOutcome:   nil,
			expectedError:     errors.New("observations are not identical: mismatch found at index 1"),
		},
		{
			name: "map identical: not all identical (different nested value)",
			observations: []values.Value{
				mustNewMap(t, map[string]any{"a": map[string]any{"x": 1}}),
				mustNewMap(t, map[string]any{"a": map[string]any{"x": 2}}),
			},
			finalSelectedType: TypeMap,
			expectedOutcome:   nil,
			expectedError:     errors.New("observations are not identical: mismatch found at index 1"),
		},
		{
			name: "map identical: not all identical (different lengths)",
			observations: []values.Value{
				mustNewMap(t, map[string]any{"a": 1}),
				mustNewMap(t, map[string]any{"a": 1, "b": 2}),
			},
			finalSelectedType: TypeMap,
			expectedOutcome:   nil,
			expectedError:     errors.New("observations are not identical: mismatch found at index 1"),
		},
		{
			name: "map identical: nil vs non-nil value in field",
			observations: []values.Value{
				mustNewMap(t, map[string]any{"a": 1, "b": nil}),
				mustNewMap(t, map[string]any{"a": 1, "b": "val"}),
			},
			finalSelectedType: TypeMap,
			expectedOutcome:   nil,
			expectedError:     errors.New("observations are not identical: mismatch found at index 1"),
		},
		{
			name: "list identical: simple lists, all identical",
			observations: []values.Value{
				mustNewList(t, []any{1, "two"}),
				mustNewList(t, []any{1, "two"}),
			},
			finalSelectedType: TypeList,
			expectedOutcome:   values.Proto(mustNewList(t, []any{1, "two"})),
			expectedError:     nil,
		},
		{
			name: "list identical: nested lists, all identical",
			observations: []values.Value{
				mustNewList(t, []any{1, []any{"x", "y"}}),
				mustNewList(t, []any{1, []any{"x", "y"}}),
			},
			finalSelectedType: TypeList,
			expectedOutcome:   values.Proto(mustNewList(t, []any{1, []any{"x", "y"}})),
			expectedError:     nil,
		},
		{
			name: "list identical: lists with maps, all identical",
			observations: []values.Value{
				mustNewList(t, []any{1, map[string]any{"a": 1}}),
				mustNewList(t, []any{1, map[string]any{"a": 1}}),
			},
			finalSelectedType: TypeList,
			expectedOutcome:   values.Proto(mustNewList(t, []any{1, map[string]any{"a": 1}})),
			expectedError:     nil,
		},
		{
			name: "list identical: lists with nil element, all identical",
			observations: []values.Value{
				mustNewList(t, []any{1, nil, "test"}),
				mustNewList(t, []any{1, nil, "test"}),
			},
			finalSelectedType: TypeList,
			expectedOutcome:   values.Proto(mustNewList(t, []any{1, nil, "test"})),
			expectedError:     nil,
		},
		{
			name: "list identical: not all identical (different value)",
			observations: []values.Value{
				mustNewList(t, []any{1, 2}),
				mustNewList(t, []any{1, 3}),
			},
			finalSelectedType: TypeList,
			expectedOutcome:   nil,
			expectedError:     errors.New("observations are not identical: mismatch found at index 1"),
		},
		{
			name: "list identical: not all identical (different length)",
			observations: []values.Value{
				mustNewList(t, []any{1, 2}),
				mustNewList(t, []any{1}),
			},
			finalSelectedType: TypeList,
			expectedOutcome:   nil,
			expectedError:     errors.New("observations are not identical: mismatch found at index 1"),
		},
		{
			name: "list identical: not all identical (different nested list)",
			observations: []values.Value{
				mustNewList(t, []any{[]any{1, 2}, 3}),
				mustNewList(t, []any{[]any{1, 3}, 3}),
			},
			finalSelectedType: TypeList,
			expectedOutcome:   nil,
			expectedError:     errors.New("observations are not identical: mismatch found at index 1"),
		},
		{
			name: "list identical: nil element vs non-nil",
			observations: []values.Value{
				mustNewList(t, []any{1, nil}),
				mustNewList(t, []any{1, 2}),
			},
			finalSelectedType: TypeList,
			expectedOutcome:   nil,
			expectedError:     errors.New("observations are not identical: mismatch found at index 1"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			oh := newObservationHandler()
			outcome, err := oh.handleIdenticalAggregation(
				tc.observations,
				tc.finalSelectedType,
			)

			if tc.expectedError != nil {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedError.Error(), "Error message mismatch")
			} else {
				require.NoError(t, err, "Unexpected error for test case %s", tc.name)
			}

			if tc.expectedError == nil {
				require.True(t, proto.Equal(outcome, tc.expectedOutcome),
					"Outcome mismatch for %s\nExpected: %+v\nActual:   %+v", tc.name, tc.expectedOutcome, outcome)
			}
		})
	}
}

// Test_handleCommonPrefixAggregation tests the handleCommonPrefixAggregation function directly.
func Test_handleCommonPrefixAggregation(t *testing.T) {
	t.Run("common prefix aggregation: not yet supported", func(t *testing.T) {
		observations := []values.Value{values.NewString("test")}
		finalSelectedType := TypeString
		oh := newObservationHandler()
		outcome, err := oh.handleCommonPrefixAggregation(observations, finalSelectedType)
		assert.Nil(t, outcome)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "common prefix aggregation type not supported")
	})
}

// Test_handleCommonSuffixAggregation tests the handleCommonSuffixAggregation function directly.
func Test_handleCommonSuffixAggregation(t *testing.T) {
	t.Run("common suffix aggregation: not yet supported", func(t *testing.T) {
		observations := []values.Value{values.NewString("test")}
		finalSelectedType := TypeString
		oh := newObservationHandler()
		outcome, err := oh.handleCommonSuffixAggregation(observations, finalSelectedType)
		assert.Nil(t, outcome)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "common suffix aggregation type not supported")
	})
}

func Test_countTypes(t *testing.T) {
	type testCase struct {
		name           string
		observations   []*valuespb.Value
		expectedCounts map[string]int
	}

	testCases := []testCase{
		{
			name:           "empty slice",
			observations:   []*valuespb.Value{},
			expectedCounts: map[string]int{},
		},
		{
			name: "slice with single type (int64)",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(1)),
				values.Proto(values.NewInt64(2)),
				values.Proto(values.NewInt64(3)),
			},
			expectedCounts: map[string]int{TypeInt64: 3},
		},
		{
			name: "slice with mixed types",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(1)),
				values.Proto(values.NewFloat64(1.0)),
				values.Proto(values.NewInt64(2)),
				values.Proto(values.NewString("hello")),
				values.Proto(values.NewFloat64(2.0)),
			},
			expectedCounts: map[string]int{
				TypeInt64:   2,
				TypeFloat64: 2,
				TypeString:  1,
			},
		},
		{
			name: "slice with nil values",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(1)),
				values.Proto(nil),
				values.Proto(values.NewFloat64(1.0)),
				values.Proto(nil),
				values.Proto(values.NewInt64(2)),
			},
			expectedCounts: map[string]int{
				TypeInt64:   2,
				TypeFloat64: 1,
				TypeNil:     2,
			},
		},
		{
			name: "slice with only nil values",
			observations: []*valuespb.Value{
				values.Proto(nil),
				values.Proto(nil),
			},
			expectedCounts: map[string]int{
				TypeNil: 2,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			counts := countTypes(tc.observations)
			assert.Equal(t, tc.expectedCounts, counts)
		})
	}
}

func assertDeepEqualValuesSlice(t *testing.T, expected, actual []values.Value) {
	require.Len(t, actual, len(expected), "Slice length mismatch")
	for i := range expected {
		expectedUnwrapped, err := values.Unwrap(expected[i])
		require.NoError(t, err)
		actualUnwrapped, err := values.Unwrap(actual[i])
		require.NoError(t, err)
		assert.Equal(t, expectedUnwrapped, actualUnwrapped, "Elements at index %d mismatch", i)
	}
}

func Test_filterObservations(t *testing.T) {
	type testCase struct {
		name                 string
		observationProtos    []*valuespb.Value
		minObservations      int
		expectedObservations []values.Value
		expectedTypeName     string
		expectedError        error
	}

	// Helper to create values.Value observations for expected output from protobufs
	createGoValuesFromProtos := func(protos []*valuespb.Value) []values.Value {
		var goValues []values.Value
		for _, p := range protos {
			v, err := values.FromProto(p)
			require.NoError(t, err)
			goValues = append(goValues, v)
		}
		return goValues
	}

	testCases := []testCase{
		{
			name: "sufficient observations of dominant type (int64)",
			observationProtos: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewFloat64(1.1)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewFloat64(2.2)),
				values.Proto(values.NewInt64(30)),
			},
			minObservations: 3,
			expectedObservations: createGoValuesFromProtos([]*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(30)),
			}),
			expectedTypeName: TypeInt64,
			expectedError:    nil,
		},
		{
			name: "insufficient total observations (initial check)",
			observationProtos: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
			},
			minObservations:      3,
			expectedObservations: nil,
			expectedTypeName:     "",
			expectedError:        errors.New("insufficient observations (2) to meet minimum (3)"),
		},
		{
			name: "no dominant type meeting threshold",
			observationProtos: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewFloat64(1.1)),
				values.Proto(values.NewFloat64(2.2)),
			},
			minObservations:      3,
			expectedObservations: nil,
			expectedTypeName:     "",
			expectedError:        errors.New("no single type met the minimum observation threshold of 3"),
		},
		{
			name: "dominant type is TypeNil",
			observationProtos: []*valuespb.Value{
				values.Proto(nil),
				values.Proto(nil),
				values.Proto(values.NewInt64(10)),
			},
			minObservations:      2,
			expectedObservations: nil,
			expectedTypeName:     "",
			expectedError:        errors.New("no single type met the minimum observation threshold of 2"),
		},
		{
			name: "all observations are of dominant type",
			observationProtos: []*valuespb.Value{
				values.Proto(values.NewFloat64(1.1)),
				values.Proto(values.NewFloat64(2.2)),
				values.Proto(values.NewFloat64(3.3)),
			},
			minObservations: 3,
			expectedObservations: createGoValuesFromProtos([]*valuespb.Value{
				values.Proto(values.NewFloat64(1.1)),
				values.Proto(values.NewFloat64(2.2)),
				values.Proto(values.NewFloat64(3.3)),
			}),
			expectedTypeName: TypeFloat64,
			expectedError:    nil,
		},
		{
			name: "mixed types but only dominant passes filter",
			observationProtos: []*valuespb.Value{
				values.Proto(values.NewInt64(100)),
				values.Proto(values.NewString("test")),
				values.Proto(values.NewInt64(200)),
				values.Proto(values.NewBool(true)),
			},
			minObservations: 2,
			expectedObservations: createGoValuesFromProtos([]*valuespb.Value{
				values.Proto(values.NewInt64(100)),
				values.Proto(values.NewInt64(200)),
			}),
			expectedTypeName: TypeInt64,
			expectedError:    nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualObservations, actualTypeName, err := filterObservations(tc.observationProtos, tc.minObservations)

			if tc.expectedError != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError.Error())
				assert.Nil(t, actualObservations)
				assert.Empty(t, actualTypeName)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedTypeName, actualTypeName)
				assertDeepEqualValuesSlice(t, tc.expectedObservations, actualObservations)
			}
		})
	}
}

// Test_compareAnyValue tests the compareAnyValue method of observationHandler.
func Test_compareAnyValue(t *testing.T) {
	type testCase struct {
		name     string
		a        any
		b        any
		expected int // -1 for a < b, 0 for a == b, 1 for a > b
	}

	// Some common values for reusability in test cases
	time1 := parseTime(t, "2023-01-01T00:00:00Z")
	time2 := parseTime(t, "2023-01-01T01:00:00Z")
	dec1 := decimal.NewFromInt(10)
	dec2 := decimal.NewFromInt(20)
	bigInt1 := big.NewInt(100)
	bigInt2 := big.NewInt(200)

	testCases := []testCase{
		// Primitive Types - Equal
		{"int64 equal", int64(10), int64(10), 0},
		{"float64 equal", float64(10.5), float64(10.5), 0},
		{"decimal equal", dec1, dec1.Copy(), 0}, // Use Copy for distinct objects
		{"big.Int equal", bigInt1, new(big.Int).Set(bigInt1), 0},
		{"time equal", time1, time1, 0},
		{"string equal", "hello", "hello", 0},
		{"bool equal (true)", true, true, 0},
		{"bool equal (false)", false, false, 0},
		{"bytes equal", []byte("data"), []byte("data"), 0},

		// Primitive Types - Not Equal
		{"int64 not equal (a<b)", int64(10), int64(20), -1},
		{"int64 not equal (a>b)", int64(20), int64(10), 1},
		{"float64 not equal (a<b)", float64(10.5), float64(20.5), -1},
		{"float64 not equal (a>b)", float64(20.5), float64(10.5), 1},
		{"decimal not equal (a<b)", dec1, dec2, -1},
		{"decimal not equal (a>b)", dec2, dec1, 1},
		{"big.Int not equal (a<b)", bigInt1, bigInt2, -1},
		{"big.Int not equal (a>b)", bigInt2, bigInt1, 1},
		{"time not equal (a<b)", time1, time2, -1},
		{"time not equal (a>b)", time2, time1, 1},
		{"string not equal (a<b)", "apple", "banana", -1},
		{"string not equal (a>b)", "banana", "apple", 1},
		{"bool not equal (false<true)", false, true, -1},
		{"bool not equal (true>false)", true, false, 1},
		{"bytes not equal (a<b)", []byte("apple"), []byte("banana"), -1},
		{"bytes not equal (a>b)", []byte("banana"), []byte("apple"), 1},

		// Nil Comparisons
		{"both nil", nil, nil, 0},
		{"nil vs int64", nil, int64(10), -1},
		{"int64 vs nil", int64(10), nil, -1},
		{"nil vs different type nil (e.g. *string(nil) vs *int(nil))", (*string)(nil), (*int)(nil), -1},
		{"nil map vs nil map", (map[string]any)(nil), (map[string]any)(nil), 0},
		{"nil slice vs nil slice", ([]any)(nil), ([]any)(nil), 0},
		{"nil vs empty slice", nil, []any{}, -1},
		{"empty slice vs nil", []any{}, nil, -1},

		// Type Mismatch (types are fundamentally different, not just values)
		{"int64 vs string", int64(10), "hello", -1},
		{"bool vs float64", true, float64(1.0), -1},
		{"map vs string", map[string]any{"a": 1}, "test", -1},
		{"list vs int64", []any{1, 2}, int64(3), -1},

		// Nested Maps - Equal
		{
			"map nested equal",
			map[string]any{"a": int64(1), "b": map[string]any{"c": "d"}},
			map[string]any{"a": int64(1), "b": map[string]any{"c": "d"}},
			0,
		},
		{
			"map nested equal with list",
			map[string]any{"a": int64(1), "b": []any{"x", "y"}},
			map[string]any{"a": int64(1), "b": []any{"x", "y"}},
			0,
		},
		{
			"map nested equal with nil value",
			map[string]any{"a": int64(1), "b": nil},
			map[string]any{"a": int64(1), "b": nil},
			0,
		},

		// Nested Maps - Not Equal
		{
			"map different value",
			map[string]any{"a": int64(1)},
			map[string]any{"a": int64(2)},
			-1,
		},
		{
			"map different key",
			map[string]any{"a": int64(1)},
			map[string]any{"b": int64(1)},
			-1,
		},
		{
			"map different nested map",
			map[string]any{"a": map[string]any{"c": "d"}},
			map[string]any{"a": map[string]any{"c": "e"}},
			-1,
		},
		{
			"map different nested list",
			map[string]any{"a": []any{1, 2}},
			map[string]any{"a": []any{1, 3}},
			-1,
		},
		{
			"map different lengths",
			map[string]any{"a": int64(1)},
			map[string]any{"a": int64(1), "b": int64(2)},
			-1,
		},
		{
			"map nil value vs non-nil",
			map[string]any{"a": int64(1), "b": nil},
			map[string]any{"a": int64(1), "b": int64(2)},
			-1,
		},
		{
			"map missing key in b",
			map[string]any{"a": 1, "b": 2},
			map[string]any{"a": 1},
			-1,
		},
		{
			"map extra key in b",
			map[string]any{"a": 1},
			map[string]any{"a": 1, "b": 2},
			-1,
		},

		// Nested Lists - Equal
		{
			"list nested equal",
			[]any{int64(1), []any{"x", "y"}},
			[]any{int64(1), []any{"x", "y"}},
			0,
		},
		{
			"list nested equal with map",
			[]any{int64(1), map[string]any{"x": "y"}},
			[]any{int64(1), map[string]any{"x": "y"}},
			0,
		},
		{
			"list nested equal with nil element",
			[]any{int64(1), nil, "test"},
			[]any{int64(1), nil, "test"},
			0,
		},

		// Nested Lists - Not Equal
		{
			"list different value",
			[]any{int64(1), int64(2)},
			[]any{int64(1), int64(3)},
			-1,
		},
		{
			"list different length",
			[]any{int64(1), int64(2)},
			[]any{int64(1)},
			-1,
		},
		{
			"list different nested list",
			[]any{[]any{1, 2}, 3},
			[]any{[]any{1, 3}, 3},
			-1,
		},
		{
			"list different nested map",
			[]any{map[string]any{"a": 1}},
			[]any{map[string]any{"a": 2}},
			-1,
		},
		{
			"list nil element vs non-nil",
			[]any{int64(1), nil},
			[]any{int64(1), int64(2)},
			-1,
		},

		// By Default no handling of unknown types
		{"unhandled struct type", struct{ X int }{X: 1}, struct{ X int }{X: 1}, -1},
		{"unhandled struct type different", struct{ X int }{X: 1}, struct{ X int }{X: 2}, -1},
		{"unhandled struct vs different type", struct{ X int }{X: 1}, int64(1), -1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := compareAnyValue(tc.a, tc.b, newObservationHandler().typeOperations)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func parseTime(t *testing.T, s string) time.Time {
	parsedTime, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return parsedTime
}

func mustNewMap[T any](t *testing.T, in map[string]T) *values.Map {
	out, err := values.NewMap(in)
	require.NoError(t, err)
	return out
}

func mustNewList(t *testing.T, in []any) *values.List {
	out, err := values.NewList(in)
	require.NoError(t, err)
	return out
}
