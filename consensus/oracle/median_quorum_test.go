package oracle

import (
	"errors"
	"math/big"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

// Tests for the medianQuorumFlag parameter on consensus calculation (rolled out via
// RequestObservation.median_2fplus1_quorum_flag). When enabled, median aggregation
// requires 2f+1 observations of a single type instead of f+1.

func medianDescriptor() *sdk.ConsensusDescriptor {
	return &sdk.ConsensusDescriptor{
		Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
			Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN,
		},
	}
}

// quorumExpectation is the expected result for one setting of medianQuorumFlag.
type quorumExpectation struct {
	outcome     *valuespb.Value
	errIs       error
	errContains string
}

func requireQuorumExpectation(t *testing.T, exp quorumExpectation, outcome *valuespb.Value, err error) {
	t.Helper()

	if exp.errIs == nil && exp.errContains == "" {
		require.NoError(t, err)
		require.True(t, proto.Equal(outcome, exp.outcome),
			"outcome mismatch\nExpected: %+v\nActual:   %+v", exp.outcome, outcome)
		return
	}

	require.Error(t, err)
	if exp.errIs != nil {
		require.True(t, errors.Is(err, exp.errIs), "expected %v, got %v", exp.errIs, err)
	}
	if exp.errContains != "" {
		require.Contains(t, err.Error(), exp.errContains)
	}
}

func Test_handleMedianAggregation_medianQuorumFlag(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		f            int
		observations []*valuespb.Value
		flagOff      quorumExpectation
		flagOn       quorumExpectation
	}{
		{
			name: "exactly 2f+1 observations of one type meets both quorums",
			f:    2,
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(30)),
				values.Proto(values.NewInt64(40)),
				values.Proto(values.NewInt64(50)),
			},
			flagOff: quorumExpectation{outcome: values.Proto(values.NewInt64(30))},
			flagOn:  quorumExpectation{outcome: values.Proto(values.NewInt64(30))},
		},
		{
			name: "f+1 of dominant type among 2f+1 observations no longer reaches quorum",
			f:    2,
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(30)),
				values.Proto(values.NewString("malicious")),
				values.Proto(values.NewString("malicious")),
			},
			flagOff: quorumExpectation{outcome: values.Proto(values.NewInt64(20))},
			flagOn:  quorumExpectation{errIs: ErrNoSingleValueTypeMeetsThreshold},
		},
		{
			name: "2f of dominant type among 2f+1 observations no longer reaches quorum",
			f:    2,
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(30)),
				values.Proto(values.NewInt64(40)),
				values.Proto(values.NewString("malicious")),
			},
			flagOff: quorumExpectation{outcome: values.Proto(values.NewInt64(20))},
			flagOn:  quorumExpectation{errIs: ErrNoSingleValueTypeMeetsThreshold},
		},
		{
			name: "empty observations count towards the total but not the type quorum",
			f:    2,
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(30)),
				values.Proto(values.NewInt64(40)),
				{},
			},
			flagOff: quorumExpectation{outcome: values.Proto(values.NewInt64(20))},
			flagOn:  quorumExpectation{errIs: ErrNoSingleValueTypeMeetsThreshold},
		},
		{
			name: "fewer than 2f+1 observations in total",
			f:    2,
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(30)),
				values.Proto(values.NewInt64(40)),
			},
			flagOff: quorumExpectation{outcome: values.Proto(values.NewInt64(20))},
			flagOn:  quorumExpectation{errContains: "insufficient observations (4) to meet minimum (5)"},
		},
		{
			name: "two types each reaching f+1 but neither reaching 2f+1",
			f:    1,
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewFloat64(1.0)),
				values.Proto(values.NewFloat64(2.0)),
			},
			flagOff: quorumExpectation{errIs: ErrMoreThanOneValidOutcomeForIdenticalConsensus},
			flagOn:  quorumExpectation{errIs: ErrNoSingleValueTypeMeetsThreshold},
		},
		{
			name: "two types each reaching 2f+1 remains ambiguous",
			f:    1,
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(30)),
				values.Proto(values.NewFloat64(1.0)),
				values.Proto(values.NewFloat64(2.0)),
				values.Proto(values.NewFloat64(3.0)),
			},
			flagOff: quorumExpectation{errIs: ErrMoreThanOneValidOutcomeForIdenticalConsensus},
			flagOn:  quorumExpectation{errIs: ErrMoreThanOneValidOutcomeForIdenticalConsensus},
		},
		{
			name: "more than 2f+1 observations of one type takes the median of all of them",
			f:    1,
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(10)),
				values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(30)),
				values.Proto(values.NewInt64(40)),
				values.Proto(values.NewInt64(50)),
			},
			flagOff: quorumExpectation{outcome: values.Proto(values.NewInt64(30))},
			flagOn:  quorumExpectation{outcome: values.Proto(values.NewInt64(30))},
		},
		{
			name: "f=0 leaves both quorums at one observation",
			f:    0,
			observations: []*valuespb.Value{
				values.Proto(values.NewInt64(42)),
			},
			flagOff: quorumExpectation{outcome: values.Proto(values.NewInt64(42))},
			flagOn:  quorumExpectation{outcome: values.Proto(values.NewInt64(42))},
		},
		{
			name:         "no observations",
			f:            2,
			observations: []*valuespb.Value{},
			flagOff:      quorumExpectation{errContains: "insufficient observations (0) to meet minimum (3)"},
			flagOn:       quorumExpectation{errContains: "insufficient observations (0) to meet minimum (5)"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			t.Run("quorum f+1 (flag disabled)", func(t *testing.T) {
				t.Parallel()
				outcome, err := handleMedianAggregation(logger.Test(t), tc.observations, tc.f, false)
				requireQuorumExpectation(t, tc.flagOff, outcome, err)
			})

			t.Run("quorum 2f+1 (flag enabled)", func(t *testing.T) {
				t.Parallel()
				outcome, err := handleMedianAggregation(logger.Test(t), tc.observations, tc.f, true)
				requireQuorumExpectation(t, tc.flagOn, outcome, err)
			})
		})
	}
}

// Test_handleMedianAggregation_medianQuorumFlag_supportedTypes checks the raised quorum
// applies to every type median aggregation supports, not just int64. Each case supplies
// 2f of the dominant type plus one other-typed observation, so the total clears 2f+1 but
// the dominant type does not.
func Test_handleMedianAggregation_medianQuorumFlag_supportedTypes(t *testing.T) {
	t.Parallel()

	f := 2
	testCases := []struct {
		name            string
		belowQuorum     []*valuespb.Value
		atQuorum        []*valuespb.Value
		expectedOutcome *valuespb.Value
	}{
		{
			name: "int64",
			belowQuorum: []*valuespb.Value{
				values.Proto(values.NewInt64(10)), values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(30)), values.Proto(values.NewInt64(40)),
			},
			atQuorum: []*valuespb.Value{
				values.Proto(values.NewInt64(10)), values.Proto(values.NewInt64(20)),
				values.Proto(values.NewInt64(30)), values.Proto(values.NewInt64(40)),
				values.Proto(values.NewInt64(50)),
			},
			expectedOutcome: values.Proto(values.NewInt64(30)),
		},
		{
			name: "uint64",
			belowQuorum: []*valuespb.Value{
				values.Proto(values.NewUint64(10)), values.Proto(values.NewUint64(20)),
				values.Proto(values.NewUint64(30)), values.Proto(values.NewUint64(40)),
			},
			atQuorum: []*valuespb.Value{
				values.Proto(values.NewUint64(10)), values.Proto(values.NewUint64(20)),
				values.Proto(values.NewUint64(30)), values.Proto(values.NewUint64(40)),
				values.Proto(values.NewUint64(50)),
			},
			expectedOutcome: values.Proto(values.NewUint64(30)),
		},
		{
			name: "float64",
			belowQuorum: []*valuespb.Value{
				values.Proto(values.NewFloat64(10.5)), values.Proto(values.NewFloat64(20.5)),
				values.Proto(values.NewFloat64(30.5)), values.Proto(values.NewFloat64(40.5)),
			},
			atQuorum: []*valuespb.Value{
				values.Proto(values.NewFloat64(10.5)), values.Proto(values.NewFloat64(20.5)),
				values.Proto(values.NewFloat64(30.5)), values.Proto(values.NewFloat64(40.5)),
				values.Proto(values.NewFloat64(50.5)),
			},
			expectedOutcome: values.Proto(values.NewFloat64(30.5)),
		},
		{
			name: "decimal",
			belowQuorum: []*valuespb.Value{
				mustDecimal(10.1), mustDecimal(20.2), mustDecimal(30.3), mustDecimal(40.4),
			},
			atQuorum: []*valuespb.Value{
				mustDecimal(10.1), mustDecimal(20.2), mustDecimal(30.3), mustDecimal(40.4), mustDecimal(50.5),
			},
			expectedOutcome: mustDecimal(30.3),
		},
		{
			name: "bigint",
			belowQuorum: []*valuespb.Value{
				mustBigInt(100), mustBigInt(200), mustBigInt(300), mustBigInt(400),
			},
			atQuorum: []*valuespb.Value{
				mustBigInt(100), mustBigInt(200), mustBigInt(300), mustBigInt(400), mustBigInt(500),
			},
			expectedOutcome: mustBigInt(300),
		},
		{
			name: "time",
			belowQuorum: []*valuespb.Value{
				mustTime(t, "2023-01-01T00:00:10Z"), mustTime(t, "2023-01-01T00:00:20Z"),
				mustTime(t, "2023-01-01T00:00:30Z"), mustTime(t, "2023-01-01T00:00:40Z"),
			},
			atQuorum: []*valuespb.Value{
				mustTime(t, "2023-01-01T00:00:10Z"), mustTime(t, "2023-01-01T00:00:20Z"),
				mustTime(t, "2023-01-01T00:00:30Z"), mustTime(t, "2023-01-01T00:00:40Z"),
				mustTime(t, "2023-01-01T00:00:50Z"),
			},
			expectedOutcome: mustTime(t, "2023-01-01T00:00:30Z"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			t.Run("2f of type fails quorum", func(t *testing.T) {
				t.Parallel()
				padded := append(append([]*valuespb.Value{}, tc.belowQuorum...), values.Proto(values.NewString("malicious")))
				_, err := handleMedianAggregation(logger.Test(t), padded, f, true)
				require.Error(t, err)
				require.True(t, errors.Is(err, ErrNoSingleValueTypeMeetsThreshold), "got %v", err)
			})

			t.Run("2f+1 of type meets quorum", func(t *testing.T) {
				t.Parallel()
				outcome, err := handleMedianAggregation(logger.Test(t), tc.atQuorum, f, true)
				require.NoError(t, err)
				require.True(t, proto.Equal(outcome, tc.expectedOutcome),
					"outcome mismatch\nExpected: %+v\nActual:   %+v", tc.expectedOutcome, outcome)
			})
		})
	}
}

// Test_getMedian_medianQuorumFlag_unwrapFailures covers the post-unwrap quorum re-check:
// observations that fail to unwrap are dropped, and the remainder must still meet 2f+1.
func Test_getMedian_medianQuorumFlag_unwrapFailures(t *testing.T) {
	t.Parallel()

	f := 2
	unwrapFailure := errors.New("corrupt observation")
	// Unwraps int64 values, but treats the sentinel 0 as corrupt.
	unwrap := func(val *valuespb.Value) (int64, error) {
		if val.GetInt64Value() == 0 {
			return 0, unwrapFailure
		}
		return val.GetInt64Value(), nil
	}
	compare := func(a, b int64) int { return int(a - b) }

	t.Run("drops below 2f+1 after unwrap failures", func(t *testing.T) {
		t.Parallel()
		// 5 observations clear the initial quorum, but only 4 survive unwrapping.
		observations := []*valuespb.Value{
			values.Proto(values.NewInt64(10)),
			values.Proto(values.NewInt64(20)),
			values.Proto(values.NewInt64(30)),
			values.Proto(values.NewInt64(40)),
			values.Proto(values.NewInt64(0)),
		}

		_, err := getMedian(logger.Test(t), observations, unwrap, compare, f, true)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrInsufficientObservations), "got %v", err)
	})

	t.Run("same unwrap failures still meet f+1", func(t *testing.T) {
		t.Parallel()
		observations := []*valuespb.Value{
			values.Proto(values.NewInt64(10)),
			values.Proto(values.NewInt64(20)),
			values.Proto(values.NewInt64(30)),
			values.Proto(values.NewInt64(40)),
			values.Proto(values.NewInt64(0)),
		}

		outcome, err := getMedian(logger.Test(t), observations, unwrap, compare, f, false)
		require.NoError(t, err)
		require.True(t, proto.Equal(outcome, values.Proto(values.NewInt64(20))), "got %+v", outcome)
	})

	t.Run("stays at 2f+1 after unwrap failures", func(t *testing.T) {
		t.Parallel()
		observations := []*valuespb.Value{
			values.Proto(values.NewInt64(10)),
			values.Proto(values.NewInt64(20)),
			values.Proto(values.NewInt64(30)),
			values.Proto(values.NewInt64(40)),
			values.Proto(values.NewInt64(50)),
			values.Proto(values.NewInt64(0)),
		}

		outcome, err := getMedian(logger.Test(t), observations, unwrap, compare, f, true)
		require.NoError(t, err)
		require.True(t, proto.Equal(outcome, values.Proto(values.NewInt64(30))), "got %+v", outcome)
	})
}

// Test_CalculateOutcomeForObservations_medianQuorumFlag_otherAggregations checks the flag
// only affects median aggregation; every other aggregation type keeps its f+1 threshold.
func Test_CalculateOutcomeForObservations_medianQuorumFlag_otherAggregations(t *testing.T) {
	t.Parallel()

	f := 2
	testCases := []struct {
		name        string
		aggregation sdk.AggregationType
		// observations is rebuilt per run: common suffix aggregation reverses the
		// observation lists in place, so a slice cannot be aggregated twice.
		observations    func() []*valuespb.Value
		expectedOutcome *valuespb.Value
	}{
		{
			name:        "identical",
			aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL,
			observations: func() []*valuespb.Value {
				return []*valuespb.Value{
					values.Proto(values.NewInt64(42)),
					values.Proto(values.NewInt64(42)),
					values.Proto(values.NewInt64(42)),
					values.Proto(values.NewInt64(50)),
					values.Proto(values.NewString("malicious")),
				}
			},
			expectedOutcome: values.Proto(values.NewInt64(42)),
		},
		{
			name:        "common prefix",
			aggregation: sdk.AggregationType_AGGREGATION_TYPE_COMMON_PREFIX,
			observations: func() []*valuespb.Value {
				return []*valuespb.Value{
					mustNewList("1", "2", "3"),
					mustNewList("1", "2", "4"),
					mustNewList("1", "2", "5"),
					mustNewList("9", "9"),
					mustNewList(),
				}
			},
			expectedOutcome: mustNewList("1", "2"),
		},
		{
			name:        "common suffix",
			aggregation: sdk.AggregationType_AGGREGATION_TYPE_COMMON_SUFFIX,
			observations: func() []*valuespb.Value {
				return []*valuespb.Value{
					mustNewList("1", "8", "9"),
					mustNewList("2", "8", "9"),
					mustNewList("3", "8", "9"),
					mustNewList("9", "9"),
					mustNewList(),
				}
			},
			expectedOutcome: mustNewList("8", "9"),
		},
		{
			name:        "frequency list",
			aggregation: sdk.AggregationType_AGGREGATION_TYPE_FREQUENCY_LIST,
			observations: func() []*valuespb.Value {
				return []*valuespb.Value{
					values.Proto(values.NewInt64(42)),
					values.Proto(values.NewInt64(42)),
					values.Proto(values.NewInt64(42)),
				}
			},
			expectedOutcome: valuespb.NewListValue([]*valuespb.Value{
				valuespb.NewMapValue(map[string]*valuespb.Value{
					"value": values.Proto(values.NewInt64(42)),
					"count": valuespb.NewInt64Value(3),
				}),
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			desc := &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: tc.aggregation},
			}

			for _, medianQuorumFlag := range []bool{false, true} {
				outcome, err := CalculateOutcomeForObservations(
					logger.Test(t), tc.observations(), desc, nil, f, medianQuorumFlag)
				require.NoError(t, err, "medianQuorumFlag=%v", medianQuorumFlag)
				require.True(t, proto.Equal(outcome, tc.expectedOutcome),
					"medianQuorumFlag=%v\nExpected: %+v\nActual:   %+v", medianQuorumFlag, tc.expectedOutcome, outcome)
			}
		})
	}
}

// Test_handleFieldsMapAggregation_medianQuorumFlag checks the flag reaches median fields
// nested inside a fields map, and that a field failing the raised quorum falls back to its
// default when one is configured.
func Test_handleFieldsMapAggregation_medianQuorumFlag(t *testing.T) {
	t.Parallel()

	f := 2
	desc := map[string]*sdk.ConsensusDescriptor{
		"Val":        medianDescriptor(),
		"OtherField": {Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
	}

	// f=2 => 2f+1 quorum of 5 for Val, which reaches only 3 int64 observations.
	// OtherField is identical across all five and keeps its f+1 threshold either way.
	observations := []*valuespb.Value{
		fieldsObs(values.Proto(values.NewInt64(10)), "same"),
		fieldsObs(values.Proto(values.NewInt64(20)), "same"),
		fieldsObs(values.Proto(values.NewInt64(30)), "same"),
		fieldsObs(values.Proto(values.NewString("malicious")), "same"),
		fieldsObs(values.Proto(values.NewString("malicious")), "same"),
	}

	t.Run("flag disabled aggregates the median field at f+1", func(t *testing.T) {
		t.Parallel()
		outcome, err := handleFieldsMapAggregation(logger.Test(t), observations, desc, nil, f, false)
		require.NoError(t, err)
		expected := fieldsObs(values.Proto(values.NewInt64(20)), "same")
		require.True(t, proto.Equal(outcome, expected), "got %+v", outcome)
	})

	t.Run("flag enabled fails the median field with no default", func(t *testing.T) {
		t.Parallel()
		_, err := handleFieldsMapAggregation(logger.Test(t), observations, desc, nil, f, true)
		require.Error(t, err)
		require.Contains(t, err.Error(), "aggregation for field failed 'Val'")
		require.True(t, errors.Is(err, ErrNoSingleValueTypeMeetsThreshold), "got %v", err)
	})

	t.Run("flag enabled falls back to the default for the median field", func(t *testing.T) {
		t.Parallel()
		defaultValue := fieldsObs(values.Proto(values.NewInt64(99)), "default")
		outcome, err := handleFieldsMapAggregation(logger.Test(t), observations, desc, defaultValue, f, true)
		require.NoError(t, err)
		expected := fieldsObs(values.Proto(values.NewInt64(99)), "same")
		require.True(t, proto.Equal(outcome, expected), "got %+v", outcome)
	})
}

// Test_handleFieldsMapAggregation_medianQuorumFlag_outerThreshold checks the flag does not
// raise the fields map's own f+1 observation threshold: with 2f observations the outer check
// still passes, and only the median field fails.
func Test_handleFieldsMapAggregation_medianQuorumFlag_outerThreshold(t *testing.T) {
	t.Parallel()

	f := 2
	desc := map[string]*sdk.ConsensusDescriptor{
		"Val":        medianDescriptor(),
		"OtherField": {Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
	}

	observations := []*valuespb.Value{
		fieldsObs(values.Proto(values.NewInt64(10)), "same"),
		fieldsObs(values.Proto(values.NewInt64(20)), "same"),
		fieldsObs(values.Proto(values.NewInt64(30)), "same"),
	}

	t.Run("flag disabled succeeds at f+1 observations", func(t *testing.T) {
		t.Parallel()
		outcome, err := handleFieldsMapAggregation(logger.Test(t), observations, desc, nil, f, false)
		require.NoError(t, err)
		expected := fieldsObs(values.Proto(values.NewInt64(20)), "same")
		require.True(t, proto.Equal(outcome, expected), "got %+v", outcome)
	})

	t.Run("flag enabled passes the outer check but fails the median field", func(t *testing.T) {
		t.Parallel()
		_, err := handleFieldsMapAggregation(logger.Test(t), observations, desc, nil, f, true)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrInsufficientObservations,
			"the fields map level threshold should still be f+1")
		require.Contains(t, err.Error(), "aggregation for field failed 'Val'")
		require.Contains(t, err.Error(), "insufficient observations (3) to meet minimum (5)")
	})
}

// Test_CalculateOutcomeForObservations_medianQuorumFlag_nested checks the flag propagates
// through nested fields maps down to a median leaf field.
func Test_CalculateOutcomeForObservations_medianQuorumFlag_nested(t *testing.T) {
	t.Parallel()

	f := 2
	desc := &sdk.ConsensusDescriptor{
		Descriptor_: &sdk.ConsensusDescriptor_FieldsMap{
			FieldsMap: &sdk.FieldsMap{
				Fields: map[string]*sdk.ConsensusDescriptor{
					"Nest": {
						Descriptor_: &sdk.ConsensusDescriptor_FieldsMap{
							FieldsMap: &sdk.FieldsMap{
								Fields: map[string]*sdk.ConsensusDescriptor{
									"Inner": medianDescriptor(),
								},
							},
						},
					},
				},
			},
		},
	}

	nested := func(inner *valuespb.Value) *valuespb.Value {
		return valuespb.NewMapValue(map[string]*valuespb.Value{
			"Nest": valuespb.NewMapValue(map[string]*valuespb.Value{"Inner": inner}),
		})
	}

	// Inner reaches only 3 int64 observations, short of the 2f+1 quorum of 5.
	observations := []*valuespb.Value{
		nested(values.Proto(values.NewInt64(10))),
		nested(values.Proto(values.NewInt64(20))),
		nested(values.Proto(values.NewInt64(30))),
		nested(values.Proto(values.NewString("malicious"))),
		nested(values.Proto(values.NewString("malicious"))),
	}

	t.Run("flag disabled aggregates the nested median field", func(t *testing.T) {
		t.Parallel()
		outcome, err := CalculateOutcomeForObservations(logger.Test(t), observations, desc, nil, f, false)
		require.NoError(t, err)
		require.True(t, proto.Equal(outcome, nested(values.Proto(values.NewInt64(20)))), "got %+v", outcome)
	})

	t.Run("flag enabled fails the nested median field", func(t *testing.T) {
		t.Parallel()
		_, err := CalculateOutcomeForObservations(logger.Test(t), observations, desc, nil, f, true)
		require.Error(t, err)
		require.Contains(t, err.Error(), "aggregation for field failed 'Nest'")
		require.Contains(t, err.Error(), "aggregation for field failed 'Inner'")
		require.True(t, errors.Is(err, ErrNoSingleValueTypeMeetsThreshold), "got %v", err)
	})
}

func mustDecimal(f float64) *valuespb.Value {
	return values.Proto(values.NewDecimal(decimal.NewFromFloat(f)))
}

func mustBigInt(i int64) *valuespb.Value {
	return values.Proto(values.NewBigInt(big.NewInt(i)))
}

func mustTime(t *testing.T, s string) *valuespb.Value {
	return values.Proto(values.NewTime(parseTime(t, s)))
}

func fieldsObs(val *valuespb.Value, otherField string) *valuespb.Value {
	return valuespb.NewMapValue(map[string]*valuespb.Value{
		"Val":        val,
		"OtherField": values.Proto(values.NewString(otherField)),
	})
}
