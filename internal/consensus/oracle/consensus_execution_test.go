package oracle

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/smartcontractkit/cre-sdk-go/cre"

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

func Test_CalculateOutcomeForObservations(t *testing.T) {
	type testCase struct {
		name            string
		observations    []*valuespb.Value
		descriptor      *sdk.ConsensusDescriptor
		defaultValue    *valuespb.Value
		f               int
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
			descriptor: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN,
				},
			},
			f:               2,
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
			descriptor: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN,
				},
			},
			f:               4,
			expectedOutcome: values.Proto(values.NewInt64(30)),
			expectedError:   nil,
		},
		{
			name: "identical aggregation: happy path (int64)",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(42)),
				values.Proto(values.NewInt64(42)),
				values.Proto(values.NewInt64(42)),
				values.Proto(values.NewInt64(42)),
				values.Proto(values.NewInt64(50)), // spurious
				values.Proto(values.NewString("malicious")),
			},
			descriptor: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL,
				},
			},
			f:               3,
			expectedOutcome: values.Proto(values.NewInt64(42)),
			expectedError:   nil,
		},
		{
			name: "median: mixed types, two eligible types (int64, float64) - error returned",
			f:    1,
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)), values.Proto(values.NewFloat64(1.0)),
				values.Proto(values.NewInt64(20)), values.Proto(values.NewFloat64(2.0)),
				values.Proto(values.NewInt64(30)), values.Proto(values.NewInt64(40)),
			},
			descriptor: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN,
				},
			},
			expectedError: ErrMoreThanOneValidOutcomeForIdenticalConsensus,
		},
		{
			name: "median: nil value provided",
			f:    1,
			observations: []*valuespb.Value{
				valuespb.NewMapValue(map[string]*valuespb.Value{}),
				valuespb.NewMapValue(map[string]*valuespb.Value{}),
				valuespb.NewMapValue(map[string]*valuespb.Value{}),
			},
			descriptor: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_FieldsMap{
					FieldsMap: &sdk.FieldsMap{
						Fields: map[string]*sdk.ConsensusDescriptor{
							"price": &sdk.ConsensusDescriptor{
								Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
									Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN,
								},
							},
						},
					},
				},
			},
			expectedError: errors.New("insufficient observations (0) to meet minimum (2)"),
		},
		{
			name: "median: mixed types, one eligible type (int64) - handled by filtering",
			f:    2,
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)), values.Proto(values.NewFloat64(1.0)),
				values.Proto(values.NewInt64(20)), values.Proto(values.NewFloat64(2.0)),
				values.Proto(values.NewInt64(30)), values.Proto(values.NewInt64(40)),
			},
			descriptor: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN,
				},
			},
			expectedOutcome: values.Proto(values.NewInt64(20)),
			expectedError:   nil,
		},
		{
			name: "common prefix",
			observations: []*valuespb.Value{
				mustNewList("1", "2", "3", "4"),
				mustNewList("1", "2", "3", "5"),
				mustNewList("1", "2", "3", "6"),
				mustNewList("1", "2", "3", "7"),
				mustNewList(),
				mustNewList(42, 43, 44, 45),
			},
			f: 3,
			descriptor: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_COMMON_PREFIX,
				},
			},
			expectedOutcome: mustNewList("1", "2", "3"),
		},
		{
			name: "fields map",
			observations: []*valuespb.Value{
				mustWrap(s{Val: 42}),
				mustWrap(s{Val: 43}),
				mustWrap(s{Val: 43}),
				mustWrap(s{Val: 44}),
				mustWrap(s{Val: 44}),
			},
			f:               2,
			descriptor:      cre.ConsensusAggregationFromTags[s]().Descriptor(),
			expectedOutcome: mustWrap(s{Val: 43}),
		},
		{
			name: "fields map one field succeeds one field returns default",
			observations: []*valuespb.Value{ // identical consensus fails for OtherField
				mustWrap(s{Val: 42, OtherField: "A"}),
				mustWrap(s{Val: 43, OtherField: "B"}),
				mustWrap(s{Val: 43, OtherField: "C"}),
				mustWrap(s{Val: 44, OtherField: "D"}),
				mustWrap(s{Val: 44, OtherField: "E"}),
			},
			f:               2,
			descriptor:      cre.ConsensusAggregationFromTags[s]().Descriptor(),
			defaultValue:    mustWrap(s{OtherField: "Z"}),
			expectedOutcome: mustWrap(s{Val: 43, OtherField: "Z"}),
		},
		{
			name: "fields map: all fields succeed with diverse types and aggregations",
			observations: []*valuespb.Value{
				mustWrap(s{Val: 10, OtherField: "common", PrefixSlice: []int64{1, 2, 3}, Nest: s1{Val: 100}, SuffixSlice: []int64{0, 2, 3}}),
				mustWrap(s{Val: 20, OtherField: "common", PrefixSlice: []int64{1, 2, 4}, Nest: s1{Val: 100}, SuffixSlice: []int64{10, 2, 3}}),
				mustWrap(s{Val: 30, OtherField: "common", PrefixSlice: []int64{1, 2, 5}, Nest: s1{Val: 100}, SuffixSlice: []int64{100, 2, 3}}),
				mustWrap(s{Val: 40, OtherField: "common", PrefixSlice: []int64{1, 9, 8}, Nest: s1{Val: 101}, SuffixSlice: []int64{99, 2, 3}}),
				mustWrap(s{Val: 50, OtherField: "common", PrefixSlice: []int64{1, 2, 6}, Nest: s1{Val: 102}, SuffixSlice: []int64{42, 2, 3}}),
			},
			descriptor: cre.ConsensusAggregationFromTags[s]().Descriptor(),
			f:          2,
			expectedOutcome: mustWrap(s{
				Val:         30,
				OtherField:  "common",
				PrefixSlice: []int64{1, 2},
				SuffixSlice: []int64{2, 3},
				Nest:        s1{Val: 100},
			}),
			expectedError: nil,
		},
		{
			name: "common suffix",
			observations: []*valuespb.Value{
				mustNewList("1", "2", "3", "4", "5", "6", "7", "8", "9"),
				mustNewList("1", "2", "3", "4", "11", "42", "42", "7", "8", "9"),
				mustNewList("1", "2", "3", "4", "8", "9", "42", "7", "8", "9"),
				mustNewList("1", "2", "3", "100", "99", "7", "8", "9"),
				mustNewList("1", "2", "3", "10", "99", "7", "8", "9"),
				mustNewList("1", "2", "3", "110", "99", "7", "8", "9"),
				mustNewList("1", "2", "3", "1000", "99", "7", "8", "9"),
				mustNewList("1", "2", "3", "x", "99"),
				mustNewList("1", "2", "3", "err", "99"),
				mustNewList("1", "2", "3", "4", "err", "7", "8", "9"),
				mustNewList(),
				mustNewList(42, 44, 45, 46),
				mustNewList("bad", "values", "bad", "values"),
			},
			f: 7,
			descriptor: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_COMMON_SUFFIX,
				},
			},
			expectedOutcome: mustNewList("7", "8", "9"),
		},
		{
			name: "value counts aggregation: distinct values with counts",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(42)),
				values.Proto(values.NewInt64(42)),
				values.Proto(values.NewInt64(42)),
				values.Proto(values.NewInt64(50)),
				values.Proto(values.NewString("malicious")),
			},
			descriptor: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_FREQUENCY_LIST,
				},
			},
			f: 2,
			expectedOutcome: valuespb.NewListValue([]*valuespb.Value{
				valuespb.NewMapValue(map[string]*valuespb.Value{
					"value": values.Proto(values.NewInt64(42)),
					"count": valuespb.NewInt64Value(3),
				}),
				valuespb.NewMapValue(map[string]*valuespb.Value{
					"value": values.Proto(values.NewString("malicious")),
					"count": valuespb.NewInt64Value(1),
				}),
				valuespb.NewMapValue(map[string]*valuespb.Value{
					"value": values.Proto(values.NewInt64(50)),
					"count": valuespb.NewInt64Value(1),
				}),
			}),
		},
		{
			name: "value counts aggregation: insufficient observations",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
			},
			descriptor: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_FREQUENCY_LIST,
				},
			},
			f:             2,
			expectedError: ErrInsufficientObservations,
		},
		{
			name: "unknown aggregation type (UNSPECIFIED)",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
			},
			descriptor: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_UNSPECIFIED,
				},
			},
			expectedOutcome: nil,
			expectedError:   errors.New("unknown aggregation type"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, err := CalculateOutcomeForObservations(
				logger.Test(t),
				tc.observations,
				tc.descriptor,
				tc.defaultValue,
				tc.f,
				false,
			)

			if tc.expectedError != nil {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedError.Error(), "Error message mismatch")
			} else {
				require.NoError(t, err, "Unexpected error for test case %s", tc.name)
			}

			if tc.expectedError == nil {
				require.True(t, proto.Equal(outcome, tc.expectedOutcome),
					"Outcome mismatch for test: %s\nExpected: %+v\nActual:   %+v", tc.name, tc.expectedOutcome, outcome)
			}
		})
	}
}

// Test_handleMedianAggregation tests the handleMedianAggregation function directly.
func Test_handleMedianAggregation(t *testing.T) {
	type testCase struct {
		name            string
		observations    []*valuespb.Value
		expectedOutcome *valuespb.Value
		expectedError   error
		f               int
	}

	testCases := []testCase{
		{
			name: "int64 median: basic five values",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(30)), values.Proto(values.NewInt64(40)), values.Proto(values.NewInt64(10)), values.Proto(values.NewInt64(20)), values.Proto(values.NewInt64(50)),
			},
			expectedOutcome: values.Proto(values.NewInt64(30)),
			expectedError:   nil,
			f:               2,
		},
		{
			name: "int64 median: even number of values returns left value",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)), values.Proto(values.NewInt64(20)), values.Proto(values.NewInt64(30)), values.Proto(values.NewInt64(40)),
			},
			expectedOutcome: values.Proto(values.NewInt64(20)),
			expectedError:   nil,
			f:               1,
		},
		{
			name: "uint64 median: basic five values",
			observations: []*valuespb.Value{
				values.Proto(values.NewUint64(30)), values.Proto(values.NewUint64(40)), values.Proto(values.NewUint64(10)), values.Proto(values.NewUint64(20)), values.Proto(values.NewUint64(50)),
			},
			expectedOutcome: values.Proto(values.NewUint64(30)),
			expectedError:   nil,
			f:               2,
		},
		{
			name: "uint64 median: even number of values returns left value",
			observations: []*valuespb.Value{
				values.Proto(values.NewUint64(10)), values.Proto(values.NewUint64(20)), values.Proto(values.NewUint64(30)), values.Proto(values.NewUint64(40)),
			},
			expectedOutcome: values.Proto(values.NewUint64(20)),
			expectedError:   nil,
			f:               1,
		},
		{
			name: "float64 median: basic five values",
			observations: []*valuespb.Value{
				values.Proto(values.NewFloat64(30.5)), values.Proto(values.NewFloat64(40.5)), values.Proto(values.NewFloat64(10.5)), values.Proto(values.NewFloat64(20.5)), values.Proto(values.NewFloat64(50.5)),
			},
			expectedOutcome: values.Proto(values.NewFloat64(30.5)),
			expectedError:   nil,
			f:               2,
		},
		{
			name: "float64 median: negative NaN",
			observations: []*valuespb.Value{
				values.Proto(values.NewFloat64(35.5)),
				values.Proto(values.NewFloat64(40.5)),
				values.Proto(values.NewFloat64(math.Float64frombits(0xFFF8000000000001))), // Negative NaN
				values.Proto(values.NewFloat64(20.5)),
				values.Proto(values.NewFloat64(10.5)),
			},
			expectedOutcome: values.Proto(values.NewFloat64(20.5)),
			expectedError:   nil,
			f:               2,
		},
		{
			name: "float64 median: positive NaN",
			observations: []*valuespb.Value{
				values.Proto(values.NewFloat64(30.5)),
				values.Proto(values.NewFloat64(40.5)),
				values.Proto(values.NewFloat64(math.Float64frombits(0x7FF8000000000001))), // positive NaN
				values.Proto(values.NewFloat64(20.5)),
				values.Proto(values.NewFloat64(10.5)),
			},
			expectedOutcome: values.Proto(values.NewFloat64(30.5)),
			expectedError:   nil,
			f:               2,
		},
		{
			name: "float64 median: even number of values returns left value",
			observations: []*valuespb.Value{
				values.Proto(values.NewFloat64(10.5)), values.Proto(values.NewFloat64(20.5)), values.Proto(values.NewFloat64(30.5)), values.Proto(values.NewFloat64(40.5)),
			},
			expectedOutcome: values.Proto(values.NewFloat64(20.5)),
			expectedError:   nil,
			f:               1,
		},
		{
			name: "decimal median: basic five values",
			observations: []*valuespb.Value{
				values.Proto(values.NewDecimal(decimal.NewFromFloat(30.3))), values.Proto(values.NewDecimal(decimal.NewFromFloat(40.4))),
				values.Proto(values.NewDecimal(decimal.NewFromFloat(10.1))), values.Proto(values.NewDecimal(decimal.NewFromFloat(20.2))),
				values.Proto(values.NewDecimal(decimal.NewFromFloat(50.5))),
			},
			expectedOutcome: values.Proto(values.NewDecimal(decimal.NewFromFloat(30.3))),
			expectedError:   nil,
			f:               2,
		},
		{
			name: "decimal median: even number of values returns left value",
			observations: []*valuespb.Value{
				values.Proto(values.NewDecimal(decimal.NewFromFloat(10.1))), values.Proto(values.NewDecimal(decimal.NewFromFloat(20.2))),
				values.Proto(values.NewDecimal(decimal.NewFromFloat(30.3))), values.Proto(values.NewDecimal(decimal.NewFromFloat(40.4))),
			},

			expectedOutcome: values.Proto(values.NewDecimal(decimal.NewFromFloat(20.2))),
			expectedError:   nil,
			f:               1,
		},
		{
			name: "decimal median: nil coefficient",
			observations: []*valuespb.Value{
				{Value: &valuespb.Value_DecimalValue{DecimalValue: &valuespb.Decimal{}}},
				{Value: &valuespb.Value_DecimalValue{DecimalValue: &valuespb.Decimal{}}},
				{Value: &valuespb.Value_DecimalValue{DecimalValue: &valuespb.Decimal{}}},
			},
			expectedOutcome: nil,
			expectedError:   errors.New("failed to calculate decimal median: insufficient observations to reach consensus"),
			f:               1,
		},
		{
			name: "decimal median: extreme exponent is excluded",
			observations: []*valuespb.Value{
				valuespb.NewDecimalValue(decimal.New(1, 0)),
				valuespb.NewDecimalValue(decimal.New(2, 0)),
				valuespb.NewDecimalValue(decimal.New(3, 0)),
				{Value: &valuespb.Value_DecimalValue{DecimalValue: &valuespb.Decimal{Coefficient: valuespb.NewBigIntFromInt(big.NewInt(1)), Exponent: math.MaxInt32}}},
			},
			expectedOutcome: valuespb.NewDecimalValue(decimal.New(2, 0)),
			f:               1,
		},
		{
			name: "bigint median: basic five values",
			observations: []*valuespb.Value{
				values.Proto(values.NewBigInt(big.NewInt(300))), values.Proto(values.NewBigInt(big.NewInt(400))),
				values.Proto(values.NewBigInt(big.NewInt(100))), values.Proto(values.NewBigInt(big.NewInt(200))),
				values.Proto(values.NewBigInt(big.NewInt(500))),
			},
			expectedOutcome: values.Proto(values.NewBigInt(big.NewInt(300))),
			expectedError:   nil,
			f:               2,
		},
		{
			name: "bigint median: even number of values returns left value",
			observations: []*valuespb.Value{
				values.Proto(values.NewBigInt(big.NewInt(100))), values.Proto(values.NewBigInt(big.NewInt(200))),
				values.Proto(values.NewBigInt(big.NewInt(300))), values.Proto(values.NewBigInt(big.NewInt(400))),
			},
			expectedOutcome: values.Proto(values.NewBigInt(big.NewInt(200))),
			expectedError:   nil,
			f:               1,
		},
		{
			name: "time median: basic five values",
			observations: []*valuespb.Value{
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:30Z"))),
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:40Z"))),
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:10Z"))),
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:20Z"))),
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:50Z"))),
			},
			expectedOutcome: values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:30Z"))),
			expectedError:   nil,
			f:               2,
		},
		{
			name: "time median: even number of values returns left value",
			observations: []*valuespb.Value{
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:10Z"))),
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:20Z"))),
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:30Z"))),
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:40Z"))),
			},
			expectedOutcome: values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:20Z"))),
			expectedError:   nil,
			f:               1,
		},
		{
			name: "median: unsupported type for median aggregation (string)",
			observations: []*valuespb.Value{
				values.Proto(values.NewString("foo")),
				values.Proto(values.NewString("bar")),
				values.Proto(values.NewString("baz")),
				values.Proto(values.NewString("bah")),
				values.Proto(values.NewString("cad")),
			},
			expectedOutcome: nil,
			expectedError:   errors.New("unsupported type for median aggregation: " + typeString.Name()),
			f:               2,
		},
		{
			name:            "empty filtered observations for median",
			observations:    []*valuespb.Value{},
			expectedOutcome: nil,
			expectedError:   errors.New("insufficient observations"),
			f:               2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, err := handleMedianAggregation(
				logger.Test(t),
				tc.observations,
				tc.f,
				false,
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

func parseTime(t *testing.T, s string) time.Time {
	parsedTime, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return parsedTime
}

func mustWrap(v any) *valuespb.Value {
	wrapped, err := values.Wrap(v)
	if err != nil {
		panic(err)
	}
	return values.Proto(wrapped)
}

// Test_FieldsMapAggregation_ErrorDeterminism verifies that handleFieldsMapAggregation
// always returns the same error when multiple fields fail aggregation with no defaults.
// The desc map keys are iterated in sorted order so the first-failing key is stable.
func Test_FieldsMapAggregation_ErrorDeterminism(t *testing.T) {
	lggr := logger.Test(t)
	f := 2

	observations := make([]*valuespb.Value, 2*f+1)
	for i := range observations {
		observations[i] = valuespb.NewMapValue(map[string]*valuespb.Value{
			"Alpha":   values.Proto(values.NewString("unique-" + string(rune('A'+i)))),
			"Beta":    values.Proto(values.NewString("unique-" + string(rune('Z'-i)))),
			"Gamma":   values.Proto(values.NewString("unique-" + string(rune('a'+i)))),
			"Delta":   values.Proto(values.NewString("unique-" + string(rune('z'-i)))),
			"Epsilon": values.Proto(values.NewString("unique-" + string(rune('0'+i)))),
		})
	}

	desc := map[string]*sdk.ConsensusDescriptor{
		"Alpha":   {Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
		"Beta":    {Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
		"Gamma":   {Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
		"Delta":   {Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
		"Epsilon": {Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
	}

	seenErrors := map[string]bool{}
	for range 200 {
		_, err := handleFieldsMapAggregation(lggr, observations, desc, nil, f, false)
		require.Error(t, err)
		seenErrors[err.Error()] = true
	}

	require.Equal(t, 1, len(seenErrors),
		"handleFieldsMapAggregation must return a deterministic error regardless of map iteration order")
	require.Contains(t, firstKey(seenErrors), "aggregation for field failed 'Alpha'",
		"sorted iteration should always fail on the alphabetically first key")
}

func firstKey(m map[string]bool) string {
	for k := range m {
		return k
	}
	return ""
}

func FuzzCalculateOutcomeForObservations(f *testing.F) {
	aggregation := func(a sdk.AggregationType) *sdk.ConsensusDescriptor {
		return &sdk.ConsensusDescriptor{Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: a}}
	}
	fieldsMap := func(fields map[string]*sdk.ConsensusDescriptor) *sdk.ConsensusDescriptor {
		return &sdk.ConsensusDescriptor{Descriptor_: &sdk.ConsensusDescriptor_FieldsMap{FieldsMap: &sdk.FieldsMap{Fields: fields}}}
	}
	mustMarshal := func(m proto.Message) []byte {
		b, err := proto.Marshal(m)
		require.NoError(f, err)
		return b
	}
	observationsOf := func(obs ...*valuespb.Value) []byte {
		return mustMarshal(&valuespb.List{Fields: obs})
	}

	ints := observationsOf(valuespb.NewInt64Value(1), valuespb.NewInt64Value(2), valuespb.NewInt64Value(3))
	lists := observationsOf(
		valuespb.NewListValue([]*valuespb.Value{valuespb.NewStringValue("a"), valuespb.NewStringValue("b")}),
		valuespb.NewListValue([]*valuespb.Value{valuespb.NewStringValue("a"), valuespb.NewStringValue("c")}),
		valuespb.NewListValue([]*valuespb.Value{valuespb.NewStringValue("a")}),
	)
	maps := observationsOf(
		valuespb.NewMapValue(map[string]*valuespb.Value{"price": valuespb.NewInt64Value(15)}),
		valuespb.NewMapValue(map[string]*valuespb.Value{"price": valuespb.NewInt64Value(25)}),
		valuespb.NewMapValue(map[string]*valuespb.Value{}),
	)
	emptyMaps := observationsOf(
		valuespb.NewMapValue(map[string]*valuespb.Value{}),
		valuespb.NewMapValue(map[string]*valuespb.Value{}),
		valuespb.NewMapValue(map[string]*valuespb.Value{}),
	)
	defaultPrice := mustMarshal(valuespb.NewMapValue(map[string]*valuespb.Value{"price": valuespb.NewInt64Value(0)}))
	priceMedian := mustMarshal(fieldsMap(map[string]*sdk.ConsensusDescriptor{
		"price": aggregation(sdk.AggregationType_AGGREGATION_TYPE_MEDIAN),
	}))

	// Byte-level mutation rarely synthesizes nested messages like Decimal or Timestamp,
	// so every Value variant that reaches values.FromProto needs a seed of its own.
	uints := observationsOf(valuespb.NewUInt64Value(0), valuespb.NewUInt64Value(1), valuespb.NewUInt64Value(math.MaxUint64))
	floats := observationsOf(
		valuespb.NewFloat64(math.NaN()),
		valuespb.NewFloat64(math.Inf(1)),
		valuespb.NewFloat64(math.Inf(-1)),
		valuespb.NewFloat64(math.Copysign(0, -1)),
		valuespb.NewFloat64(0),
		valuespb.NewFloat64(1.5),
	)
	decimals := observationsOf(
		valuespb.NewDecimalValue(decimal.New(15, -1)),
		valuespb.NewDecimalValue(decimal.New(-15, 1)),
		&valuespb.Value{Value: &valuespb.Value_DecimalValue{DecimalValue: &valuespb.Decimal{}}},
		&valuespb.Value{Value: &valuespb.Value_DecimalValue{DecimalValue: &valuespb.Decimal{Coefficient: &valuespb.BigInt{}, Exponent: math.MaxInt32}}},
		&valuespb.Value{Value: &valuespb.Value_DecimalValue{DecimalValue: &valuespb.Decimal{Coefficient: &valuespb.BigInt{AbsVal: []byte{1}, Sign: -1}, Exponent: math.MinInt32}}},
	)
	bigints := observationsOf(
		valuespb.NewBigIntValue(0, nil),
		valuespb.NewBigIntValue(-1, []byte{1}),
		valuespb.NewBigIntValue(1, []byte{0, 0, 1}),
		valuespb.NewBigIntValue(1, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}),
		&valuespb.Value{Value: &valuespb.Value_BigintValue{}},
	)
	times := observationsOf(
		valuespb.NewTime(time.Unix(0, 0)),
		valuespb.NewTime(time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)),
		&valuespb.Value{Value: &valuespb.Value_TimeValue{}},
		&valuespb.Value{Value: &valuespb.Value_TimeValue{TimeValue: &timestamppb.Timestamp{Seconds: -1, Nanos: 2e9}}},
		&valuespb.Value{Value: &valuespb.Value_TimeValue{TimeValue: &timestamppb.Timestamp{Seconds: math.MinInt64}}},
	)
	mixed := observationsOf(
		valuespb.NewInt64Value(1), valuespb.NewInt64Value(2), valuespb.NewInt64Value(3),
		valuespb.NewStringValue("a"), valuespb.NewStringValue("b"), valuespb.NewStringValue("c"),
	)
	withEmptyValues := observationsOf(
		&valuespb.Value{}, &valuespb.Value{}, &valuespb.Value{},
		valuespb.NewInt64Value(1),
		valuespb.NewListValue([]*valuespb.Value{{}, valuespb.NewStringValue("a")}),
	)

	for _, a := range sdk.AggregationType_value {
		desc := mustMarshal(aggregation(sdk.AggregationType(a)))
		f.Add(ints, desc, []byte(nil), uint8(1), false)
		f.Add(lists, desc, []byte(nil), uint8(1), true)
		for _, obs := range [][]byte{uints, floats, decimals, bigints, times, mixed, withEmptyValues} {
			f.Add(obs, desc, []byte(nil), uint8(1), false)
			f.Add(obs, desc, []byte(nil), uint8(1), true)
		}
	}
	f.Add(maps, priceMedian, defaultPrice, uint8(1), true)
	f.Add(maps, priceMedian, []byte(nil), uint8(0), false)
	f.Add(emptyMaps, priceMedian, []byte(nil), uint8(1), false)

	deterministic := proto.MarshalOptions{Deterministic: true}

	f.Fuzz(func(t *testing.T, observationsBytes, descriptorBytes, defaultBytes []byte, fault uint8, medianQuorumFlag bool) {
		var observations valuespb.List
		if err := proto.Unmarshal(observationsBytes, &observations); err != nil {
			t.Skip()
		}
		var descriptor sdk.ConsensusDescriptor
		if err := proto.Unmarshal(descriptorBytes, &descriptor); err != nil {
			t.Skip()
		}
		var defaultValue *valuespb.Value
		if defaultBytes != nil {
			defaultValue = &valuespb.Value{}
			if err := proto.Unmarshal(defaultBytes, defaultValue); err != nil {
				t.Skip()
			}
		}

		// Compared via deterministic encoding rather than proto.Equal, which treats NaN as unequal to itself.
		encode := func(vs ...*valuespb.Value) []byte {
			b, err := deterministic.Marshal(&valuespb.List{Fields: vs})
			require.NoError(t, err)
			return b
		}
		calculate := func(obs []*valuespb.Value) (*valuespb.Value, error) {
			var (
				outcome *valuespb.Value
				err     error
			)
			require.NotPanics(t, func() {
				outcome, err = CalculateOutcomeForObservations(logger.Nop(), obs, &descriptor, defaultValue, int(fault), medianQuorumFlag)
			})
			return outcome, err
		}

		obs := observations.GetFields()
		inputsBefore := encode(append(slices.Clone(obs), defaultValue)...)
		outcome, err := calculate(obs)
		require.Equal(t, inputsBefore, encode(append(slices.Clone(obs), defaultValue)...), "inputs were mutated")

		// Consensus must not depend on the order in which observations arrive.
		if len(obs) > 1 {
			reversed := slices.Clone(obs)
			slices.Reverse(reversed)
			rotated := append(slices.Clone(obs[1:]), obs[0])
			for _, permuted := range [][]*valuespb.Value{reversed, rotated} {
				permutedOutcome, permutedErr := calculate(permuted)
				require.Equal(t, fmt.Sprint(err), fmt.Sprint(permutedErr))
				require.Equal(t, encode(outcome), encode(permutedOutcome))
			}
		}
	})
}
