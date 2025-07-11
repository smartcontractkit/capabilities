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
			name: "identical aggregation: not yet supported",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
			},
			descriptor: &pb.ConsensusDescriptor{
				Descriptor_: &pb.ConsensusDescriptor_Aggregation{
					Aggregation: pb.AggregationType_AGGREGATION_TYPE_IDENTICAL,
				},
			},
			minObs:          1,
			expectedOutcome: nil,
			expectedError:   errors.New("identical aggregation type not supported"),
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
			outcome, err := CalculateOutcomeForObservations(
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
			name: "median: mixed types, one dominant (int64) - handled by filtering",
			observations: []values.Value{
				values.NewInt64(10), values.NewFloat64(1.0),
				values.NewInt64(20), values.NewFloat64(2.0),
				values.NewInt64(30), values.NewInt64(40),
			},
			finalSelectedType: TypeInt64, // Assume this was determined as dominant
			expectedOutcome:   values.Proto(values.NewInt64(20)),
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
			name: "empty filtered observations for median",
			observations: []values.Value{
				values.NewInt64(10),
			},
			finalSelectedType: TypeFloat64, // A type not present in observations
			expectedOutcome:   nil,
			expectedError:     errors.New("no valid observations for median calculation"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, err := handleMedianAggregation(
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
	t.Run("identical aggregation: not yet supported", func(t *testing.T) {
		observations := []values.Value{values.NewInt64(10)}
		finalSelectedType := TypeInt64
		outcome, err := handleIdenticalAggregation(observations, finalSelectedType)
		assert.Nil(t, outcome)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "identical aggregation type not supported")
	})
}

// Test_handleCommonPrefixAggregation tests the handleCommonPrefixAggregation function directly.
func Test_handleCommonPrefixAggregation(t *testing.T) {
	t.Run("common prefix aggregation: not yet supported", func(t *testing.T) {
		observations := []values.Value{values.NewString("test")}
		finalSelectedType := TypeString
		outcome, err := handleCommonPrefixAggregation(observations, finalSelectedType)
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
		outcome, err := handleCommonSuffixAggregation(observations, finalSelectedType)
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

func parseTime(t *testing.T, s string) time.Time {
	parsedTime, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return parsedTime
}
