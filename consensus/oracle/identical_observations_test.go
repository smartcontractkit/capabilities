package oracle

import (
	"errors"
	"testing"

	"github.com/smartcontractkit/chainlink-common/pkg/values"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHandleIdenticalAggregation(t *testing.T) {
	nowTime := timestamppb.Now()

	tests := []struct {
		name        string
		inputValues []*valuespb.Value
		wantValue   *valuespb.Value
		wantErr     string
		f           int
	}{
		{
			name:        "NOK - Empty slice",
			inputValues: []*valuespb.Value{},
			wantErr:     "input slice cannot be empty",
		},
		{
			name: "NOK - Single string value",
			inputValues: []*valuespb.Value{
				{Value: &valuespb.Value_StringValue{StringValue: "hello"}},
			},
			f:       1,
			wantErr: "no values met f+1 threshold",
		},
		{
			name: "OK - Multiple identical string values",
			inputValues: []*valuespb.Value{
				{Value: &valuespb.Value_StringValue{StringValue: "apple"}},
				{Value: &valuespb.Value_StringValue{StringValue: "apple"}},
				{Value: &valuespb.Value_StringValue{StringValue: "apple"}},
			},
			f: 1,
			wantValue: &valuespb.Value{
				Value: &valuespb.Value_StringValue{StringValue: "apple"},
			},
		},
		{
			name: "OK - Multiple identical int64 values",
			inputValues: []*valuespb.Value{
				{Value: &valuespb.Value_Int64Value{Int64Value: 100}},
				{Value: &valuespb.Value_Int64Value{Int64Value: 100}},
			},
			f: 1,
			wantValue: &valuespb.Value{
				Value: &valuespb.Value_Int64Value{Int64Value: 100},
			},
		},
		{
			name: "OK - Multiple identical boolean values",
			inputValues: []*valuespb.Value{
				{Value: &valuespb.Value_BoolValue{BoolValue: true}},
				{Value: &valuespb.Value_BoolValue{BoolValue: true}},
			},
			f: 1,
			wantValue: &valuespb.Value{
				Value: &valuespb.Value_BoolValue{BoolValue: true},
			},
		},
		{
			name: "OK - Multiple identical float64 values",
			inputValues: []*valuespb.Value{
				{Value: &valuespb.Value_Float64Value{Float64Value: 3.14}},
				{Value: &valuespb.Value_Float64Value{Float64Value: 3.14}},
			},
			f: 1,
			wantValue: &valuespb.Value{
				Value: &valuespb.Value_Float64Value{Float64Value: 3.14},
			},
		},
		{
			name: "OK - Multiple identical time values",
			inputValues: []*valuespb.Value{
				{Value: &valuespb.Value_TimeValue{TimeValue: nowTime}},
				{Value: &valuespb.Value_TimeValue{TimeValue: nowTime}},
			},
			f: 1,
			wantValue: &valuespb.Value{
				Value: &valuespb.Value_TimeValue{TimeValue: nowTime},
			},
		},
		{
			name: "NOK - Multiple different string values",
			inputValues: []*valuespb.Value{
				{Value: &valuespb.Value_StringValue{StringValue: "alpha"}},
				{Value: &valuespb.Value_StringValue{StringValue: "beta"}},
			},
			f:       1,
			wantErr: "no values met f+1 threshold",
		},
		{
			name: "NOK - Mixed types",
			inputValues: []*valuespb.Value{
				{Value: &valuespb.Value_Int64Value{Int64Value: 1}},
				{Value: &valuespb.Value_StringValue{StringValue: "two"}},
				{Value: &valuespb.Value_BoolValue{BoolValue: false}},
			},
			f:       1,
			wantErr: "no values met f+1 threshold",
		},
		{
			name: "NOK - Mixed types and multiple options",
			inputValues: []*valuespb.Value{
				{Value: &valuespb.Value_Int64Value{Int64Value: 1}},
				{Value: &valuespb.Value_Int64Value{Int64Value: 1}},
				{Value: &valuespb.Value_StringValue{StringValue: "two"}},
				{Value: &valuespb.Value_StringValue{StringValue: "two"}},
				{Value: &valuespb.Value_BoolValue{BoolValue: false}},
			},
			f:       1,
			wantErr: "not identical",
		},
		{
			name: "OK - Slice with map values",
			inputValues: []*valuespb.Value{
				{Value: &valuespb.Value_MapValue{MapValue: &valuespb.Map{
					Fields: map[string]*valuespb.Value{
						"key":  {Value: &valuespb.Value_StringValue{StringValue: "value"}},
						"key2": {Value: &valuespb.Value_Float64Value{Float64Value: 3.14}},
						"key3": {Value: &valuespb.Value_Int64Value{Int64Value: 42}},
					},
				}}},
				{Value: &valuespb.Value_MapValue{MapValue: &valuespb.Map{
					Fields: map[string]*valuespb.Value{
						"key":  {Value: &valuespb.Value_StringValue{StringValue: "value"}},
						"key2": {Value: &valuespb.Value_Float64Value{Float64Value: 3.14}},
						"key3": {Value: &valuespb.Value_Int64Value{Int64Value: 42}},
					},
				}}},
			},
			f: 1,
			wantValue: &valuespb.Value{
				Value: &valuespb.Value_MapValue{MapValue: &valuespb.Map{
					Fields: map[string]*valuespb.Value{
						"key":  {Value: &valuespb.Value_StringValue{StringValue: "value"}},
						"key2": {Value: &valuespb.Value_Float64Value{Float64Value: 3.14}},
						"key3": {Value: &valuespb.Value_Int64Value{Int64Value: 42}},
					},
				}},
			},
		},
		{
			name: "OK - Slice with list values",
			inputValues: []*valuespb.Value{
				{Value: &valuespb.Value_ListValue{ListValue: &valuespb.List{
					Fields: []*valuespb.Value{{Value: &valuespb.Value_Int64Value{Int64Value: 1}}},
				}}},
				{Value: &valuespb.Value_ListValue{ListValue: &valuespb.List{
					Fields: []*valuespb.Value{{Value: &valuespb.Value_Int64Value{Int64Value: 1}}},
				}}},
			},
			f: 1,
			wantValue: &valuespb.Value{
				Value: &valuespb.Value_ListValue{
					ListValue: &valuespb.List{
						Fields: []*valuespb.Value{
							{
								Value: &valuespb.Value_Int64Value{Int64Value: 1},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := handleIdenticalAggregation(tc.inputValues, tc.f)

			if tc.wantErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			require.True(t, proto.Equal(tc.wantValue, got))
		})
	}
}

// Test_handleCommonPrefixAggregation tests the handleCommonPrefixAggregation function directly.
func Test_handleCommonPrefixAggregation(t *testing.T) {
	t.Run("common prefix aggregation: not yet supported", func(t *testing.T) {
		observations := []*valuespb.Value{values.Proto(values.NewString("test"))}
		outcome, err := handleCommonPrefixAggregation(observations, 0)
		assert.Nil(t, outcome)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "common prefix aggregation type not supported")
	})
}

// Test_handleCommonSuffixAggregation tests the handleCommonSuffixAggregation function directly.
func Test_handleCommonSuffixAggregation(t *testing.T) {
	t.Run("common suffix aggregation: not yet supported", func(t *testing.T) {
		observations := []*valuespb.Value{values.Proto(values.NewString("test"))}
		outcome, err := handleCommonSuffixAggregation(observations, 0)
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

func assertDeepEqualValuesSlice(t *testing.T, expected, actual []*valuespb.Value) {
	require.Len(t, actual, len(expected), "Slice length mismatch")
	for i := range expected {
		assert.True(t, proto.Equal(actual[i], expected[i]))
	}
}

func Test_filterObservations(t *testing.T) {
	type testCase struct {
		name                 string
		observationProtos    []*valuespb.Value
		minObservations      int
		expectedObservations []*valuespb.Value
		expectedTypeName     string
		expectedError        error
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
			expectedObservations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(30)),
			},
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
			expectedObservations: []*valuespb.Value{
				values.Proto(values.NewFloat64(1.1)),
				values.Proto(values.NewFloat64(2.2)),
				values.Proto(values.NewFloat64(3.3)),
			},
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
			expectedObservations: []*valuespb.Value{
				values.Proto(values.NewInt64(100)),
				values.Proto(values.NewInt64(200)),
			},
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
