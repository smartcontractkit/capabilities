package oracle

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/shopspring/decimal"
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
			name: "identical aggregation: happy path (int64)",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(42)),
				values.Proto(values.NewInt64(42)),
				values.Proto(values.NewInt64(42)),
				values.Proto(values.NewInt64(42)),
				values.Proto(values.NewInt64(50)), // spurious
				values.Proto(values.NewString("malicious")),
			},
			descriptor: &pb.ConsensusDescriptor{
				Descriptor_: &pb.ConsensusDescriptor_Aggregation{
					Aggregation: pb.AggregationType_AGGREGATION_TYPE_IDENTICAL,
				},
			},
			minObs:          5,
			f:               3,
			expectedOutcome: values.Proto(values.NewInt64(42)),
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
			outcome, err := CalculateOutcomeForObservations(
				tc.observations,
				tc.descriptor,
				tc.minObs,
				tc.f,
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
		observations      []*valuespb.Value
		finalSelectedType string
		expectedOutcome   *valuespb.Value
		expectedError     error
	}

	testCases := []testCase{
		{
			name: "int64 median: basic five values",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(30)), values.Proto(values.NewInt64(40)), values.Proto(values.NewInt64(10)), values.Proto(values.NewInt64(20)), values.Proto(values.NewInt64(50)),
			},
			finalSelectedType: TypeInt64,
			expectedOutcome:   values.Proto(values.NewInt64(30)),
			expectedError:     nil,
		},
		{
			name: "int64 median: even number of values returns left value",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)), values.Proto(values.NewInt64(20)), values.Proto(values.NewInt64(30)), values.Proto(values.NewInt64(40)),
			},
			finalSelectedType: TypeInt64,
			expectedOutcome:   values.Proto(values.NewInt64(20)),
			expectedError:     nil,
		},
		{
			name: "float64 median: basic five values",
			observations: []*valuespb.Value{
				values.Proto(values.NewFloat64(30.5)), values.Proto(values.NewFloat64(40.5)), values.Proto(values.NewFloat64(10.5)), values.Proto(values.NewFloat64(20.5)), values.Proto(values.NewFloat64(50.5)),
			},
			finalSelectedType: TypeFloat64,
			expectedOutcome:   values.Proto(values.NewFloat64(30.5)),
			expectedError:     nil,
		},
		{
			name: "float64 median: even number of values returns left value",
			observations: []*valuespb.Value{
				values.Proto(values.NewFloat64(10.5)), values.Proto(values.NewFloat64(20.5)), values.Proto(values.NewFloat64(30.5)), values.Proto(values.NewFloat64(40.5)),
			},
			finalSelectedType: TypeFloat64,
			expectedOutcome:   values.Proto(values.NewFloat64(20.5)),
			expectedError:     nil,
		},
		{
			name: "decimal median: basic five values",
			observations: []*valuespb.Value{
				values.Proto(values.NewDecimal(decimal.NewFromFloat(30.3))), values.Proto(values.NewDecimal(decimal.NewFromFloat(40.4))),
				values.Proto(values.NewDecimal(decimal.NewFromFloat(10.1))), values.Proto(values.NewDecimal(decimal.NewFromFloat(20.2))),
				values.Proto(values.NewDecimal(decimal.NewFromFloat(50.5))),
			},
			finalSelectedType: TypeDecimal,
			expectedOutcome:   values.Proto(values.NewDecimal(decimal.NewFromFloat(30.3))),
			expectedError:     nil,
		},
		{
			name: "decimal median: even number of values returns left value",
			observations: []*valuespb.Value{
				values.Proto(values.NewDecimal(decimal.NewFromFloat(10.1))), values.Proto(values.NewDecimal(decimal.NewFromFloat(20.2))),
				values.Proto(values.NewDecimal(decimal.NewFromFloat(30.3))), values.Proto(values.NewDecimal(decimal.NewFromFloat(40.4))),
			},
			finalSelectedType: TypeDecimal,
			expectedOutcome:   values.Proto(values.NewDecimal(decimal.NewFromFloat(20.2))),
			expectedError:     nil,
		},
		{
			name: "bigint median: basic five values",
			observations: []*valuespb.Value{
				values.Proto(values.NewBigInt(big.NewInt(300))), values.Proto(values.NewBigInt(big.NewInt(400))),
				values.Proto(values.NewBigInt(big.NewInt(100))), values.Proto(values.NewBigInt(big.NewInt(200))),
				values.Proto(values.NewBigInt(big.NewInt(500))),
			},
			finalSelectedType: TypeBigInt,
			expectedOutcome:   values.Proto(values.NewBigInt(big.NewInt(300))),
			expectedError:     nil,
		},
		{
			name: "bigint median: even number of values returns left value",
			observations: []*valuespb.Value{
				values.Proto(values.NewBigInt(big.NewInt(100))), values.Proto(values.NewBigInt(big.NewInt(200))),
				values.Proto(values.NewBigInt(big.NewInt(300))), values.Proto(values.NewBigInt(big.NewInt(400))),
			},
			finalSelectedType: TypeBigInt,
			expectedOutcome:   values.Proto(values.NewBigInt(big.NewInt(200))),
			expectedError:     nil,
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
			finalSelectedType: TypeTime,
			expectedOutcome:   values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:30Z"))),
			expectedError:     nil,
		},
		{
			name: "time median: even number of values returns left value",
			observations: []*valuespb.Value{
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:10Z"))),
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:20Z"))),
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:30Z"))),
				values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:40Z"))),
			},
			finalSelectedType: TypeTime,
			expectedOutcome:   values.Proto(values.NewTime(parseTime(t, "2023-01-01T00:00:20Z"))),
			expectedError:     nil,
		},
		{
			name: "median: unsupported type for median aggregation (string)",
			observations: []*valuespb.Value{
				values.Proto(values.NewString("foo")), values.Proto(values.NewString("bar")), values.Proto(values.NewString("baz")),
			},
			finalSelectedType: TypeString,
			expectedOutcome:   nil,
			expectedError:     errors.New("unsupported type for median aggregation: " + TypeString),
		},
		{
			name:              "empty filtered observations for median",
			observations:      []*valuespb.Value{},
			finalSelectedType: TypeFloat64,
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

func parseTime(t *testing.T, s string) time.Time {
	parsedTime, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return parsedTime
}
