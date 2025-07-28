package oracle

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"
	"github.com/smartcontractkit/cre-sdk-go/sdk"
)

type s struct {
	Val         int64   `consensus_aggregation:"median"`
	OtherField  string  `consensus_aggregation:"identical"`
	PrefixSlice []int64 `consensus_aggregation:"common_prefix"`
	SuffixSlice []int64 `consensus_aggregation:"common_suffix"`
	Nest        s1      `consensus_aggregation:"identical"`
	InlineSlice []struct {
		PrefixSlice []int64           `consensus_aggregation:"common_prefix"`
		Data        map[string]string `consensus_aggregation:"identical"`
	} `consensus_aggregation:"identical"`
}

type s1 struct {
	Val  uint8 `consensus_aggregation:"median"`
	Nest s2    `consensus_aggregation:"identical"`
}

type s2 struct {
	Names []string `consensus_aggregation:"identical"`
}

func Test_handleFieldsMapAggregation(t *testing.T) {
	type testCase struct {
		name            string
		observations    []*valuespb.Value
		descriptor      map[string]*pb.ConsensusDescriptor
		f               int
		expectedOutcome *valuespb.Value
		expectedError   error
	}

	testCases := []testCase{
		{
			name: "single field, median aggregation",
			observations: []*valuespb.Value{
				mustWrap(s{Val: 10}),
				mustWrap(s{Val: 20}),
				mustWrap(s{Val: 30}),
				mustWrap(s{Val: 40}),
				mustWrap(s{Val: 50}),
			},
			descriptor:      sdk.ConsensusAggregationFromTags[s]().Descriptor().GetFieldsMap().Fields,
			f:               2,
			expectedOutcome: mustWrap(s{Val: 30}),
			expectedError:   nil,
		},
		{
			name: "multiple fields, mixed aggregations",
			observations: []*valuespb.Value{
				mustWrap(s{Val: 10, OtherField: "abc", PrefixSlice: []int64{1, 2, 3}, SuffixSlice: []int64{7, 8, 9}}),
				mustWrap(s{Val: 20, OtherField: "abc", PrefixSlice: []int64{1, 2, 4}, SuffixSlice: []int64{6, 8, 9}}),
				mustWrap(s{Val: 30, OtherField: "abc", PrefixSlice: []int64{1, 5, 6}, SuffixSlice: []int64{5, 8, 9}}),
				mustWrap(s{Val: 40, OtherField: "abc", PrefixSlice: []int64{1, 2, 7}, SuffixSlice: []int64{4, 8, 9}}),
				mustWrap(s{Val: 50, OtherField: "ghi", PrefixSlice: []int64{1, 2, 8}, SuffixSlice: []int64{3, 8, 9}}),
			},
			descriptor: sdk.ConsensusAggregationFromTags[s]().Descriptor().GetFieldsMap().Fields,
			f:          2,
			expectedOutcome: mustWrap(s{
				Val:         30,
				OtherField:  "abc",
				PrefixSlice: []int64{1, 2},
				SuffixSlice: []int64{8, 9},
			}),
			expectedError: nil,
		},
		{
			name: "nested identical struct",
			observations: []*valuespb.Value{
				mustWrap(s{Nest: s1{Val: 30}}),
				mustWrap(s{Nest: s1{Val: 30}}),
				mustWrap(s{Nest: s1{Val: 30}}),
				mustWrap(s{Nest: s1{Val: 30}}),
				mustWrap(s{Nest: s1{Val: 30}}),
			},
			descriptor: sdk.ConsensusAggregationFromTags[s]().Descriptor().GetFieldsMap().Fields,
			f:          2,
			expectedOutcome: mustWrap(s{
				Nest: s1{
					Val: 30,
				},
			}),
			expectedError: nil,
		},
		{
			name: "error from child aggregation",
			observations: []*valuespb.Value{
				mustWrap(s{OtherField: "A"}),
				mustWrap(s{OtherField: "B"}),
				mustWrap(s{OtherField: "C"}),
				mustWrap(s{OtherField: "D"}),
				mustWrap(s{OtherField: "E"}),
			},
			descriptor:      sdk.ConsensusAggregationFromTags[s]().Descriptor().GetFieldsMap().Fields,
			f:               2,
			expectedOutcome: nil,
			expectedError:   errors.New("aggregation for field 'OtherField' failed: no values met f+1 threshold"),
		},
		{
			name:            "no observations and no description returns empty map",
			observations:    []*valuespb.Value{},
			descriptor:      nil,
			f:               3,
			expectedOutcome: mustWrap(map[string]any{}),
			expectedError:   nil,
		},
		{
			name:          "no observations with description errors",
			observations:  []*valuespb.Value{},
			descriptor:    sdk.ConsensusAggregationFromTags[s]().Descriptor().GetFieldsMap().Fields,
			f:             3,
			expectedError: errors.New("aggregation for field"), // non-deterministic which field will error first
		},
		{
			name: "observations with non-map types are skipped",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(100)), // This will be skipped
				mustWrap(struct{}{}),               // no Val field will be skipped
				mustWrap(s{Val: 10}),
				mustWrap(s{Val: 20}),
				mustWrap(s{Val: 30}),
				mustWrap(s{Val: 40}),
				mustWrap(s{Val: 50}),
			},
			descriptor:      sdk.ConsensusAggregationFromTags[s]().Descriptor().GetFieldsMap().Fields,
			f:               2,
			expectedOutcome: mustWrap(s{Val: 30}),
			expectedError:   nil,
		},
		{
			name: "all observations are non-map types fails for descriptor",
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(1)),
				values.Proto(values.NewString("test")),
			},
			descriptor:    sdk.ConsensusAggregationFromTags[s]().Descriptor().GetFieldsMap().Fields,
			f:             1,
			expectedError: errors.New("aggregation for field"), // non-deterministic which field will error first
		},
		{
			name: "nested s2 fields map with slices",
			observations: []*valuespb.Value{
				mustWrap(s{
					InlineSlice: inlineSlice(),
					Nest:        s1{Nest: s2{Names: []string{"n1", "n2"}}},
				}),
				mustWrap(s{
					InlineSlice: inlineSlice(),
					Nest:        s1{Nest: s2{Names: []string{"n1", "n2"}}},
				}),
				mustWrap(s{
					InlineSlice: inlineSlice(),
					Nest:        s1{Nest: s2{Names: []string{"n1", "n2"}}},
				}),
				mustWrap(s{
					InlineSlice: inlineSlice(),
					Nest:        s1{Nest: s2{Names: []string{"n1", "n2"}}},
				}),
				mustWrap(s{
					InlineSlice: inlineSlice(),
					Nest:        s1{Nest: s2{Names: []string{"n1", "n2"}}},
				}),
				mustWrap(s{
					InlineSlice: inlineSlice(),
					Nest:        s1{Nest: s2{Names: []string{"n1", "n2"}}},
				}),
			},
			descriptor: sdk.ConsensusAggregationFromTags[s]().Descriptor().GetFieldsMap().GetFields(),
			f:          2,
			expectedOutcome: mustWrap(s{
				InlineSlice: inlineSlice(),
				Nest: s1{Nest: s2{
					Names: []string{"n1", "n2"},
				}},
			},
			),
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lggr := logger.Test(t)
			lggr.Debugw("descriptor", "desc", tc.descriptor, "name", tc.name)
			got, err := handleFieldsMapAggregation(lggr, tc.observations, tc.descriptor, tc.f)

			if tc.expectedError != nil {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedError.Error())
				return
			}

			require.NoError(t, err)
			require.True(t, proto.Equal(tc.expectedOutcome, got), "expected %v, got %v", tc.expectedOutcome, got)
		})
	}
}

func inlineSlice() []struct {
	PrefixSlice []int64           `consensus_aggregation:"common_prefix"`
	Data        map[string]string `consensus_aggregation:"identical"`
} {
	return []struct {
		PrefixSlice []int64           `consensus_aggregation:"common_prefix"`
		Data        map[string]string `consensus_aggregation:"identical"`
	}{
		{
			PrefixSlice: []int64{1, 2, 3},
			Data: map[string]string{
				"k1": "v1",
			},
		},

		{
			PrefixSlice: []int64{1, 2, 3},
			Data: map[string]string{
				"k1": "v1",
			},
		},

		{
			PrefixSlice: []int64{1, 2, 3},
			Data: map[string]string{
				"k1": "v1",
			},
		},
	}
}
