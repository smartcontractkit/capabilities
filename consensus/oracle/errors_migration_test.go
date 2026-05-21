package oracle

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

// Tests for the errorsMigrationFlag parameter on consensus calculation (rolled out via
// RequestObservation.update_error_handling_flag).

func Test_CalculateOutcomeForObservations_identical_errorsMigrationFlag(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	desc := &sdk.ConsensusDescriptor{
		Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
			Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL,
		},
	}
	// f=2 => need 3 identical values; five distinct values => no cluster reaches f+1.
	observations := []*valuespb.Value{
		values.Proto(values.NewInt64(0)),
		values.Proto(values.NewInt64(10)),
		values.Proto(values.NewInt64(20)),
		values.Proto(values.NewInt64(30)),
		values.Proto(values.NewInt64(40)),
	}

	t.Run("migration enabled returns ErrNoValuesMetFPlusOneThresholdForIdenticalConsensus", func(t *testing.T) {
		t.Parallel()
		_, err := CalculateOutcomeForObservations(lggr, observations, desc, nil, 2, true)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrNoValuesMetFPlusOneThresholdForIdenticalConsensus),
			"expected ErrNoValuesMetFPlusOneThresholdForIdenticalConsensus, got %v", err)
	})

	t.Run("migration disabled returns ErrNoValuesMetThreshold", func(t *testing.T) {
		t.Parallel()
		_, err := CalculateOutcomeForObservations(lggr, observations, desc, nil, 2, false)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrNoValuesMetThreshold),
			"expected ErrNoValuesMetThreshold, got %v", err)
	})
}

func Test_filterObservations_errorsMigrationFlag(t *testing.T) {
	t.Parallel()

	// Two int64 and two float64: with minObservations=3, no single type reaches the threshold.
	observationProtos := []*valuespb.Value{
		values.Proto(values.NewInt64(10)),
		values.Proto(values.NewInt64(20)),
		values.Proto(values.NewFloat64(1.0)),
		values.Proto(values.NewFloat64(2.0)),
	}

	t.Run("migration enabled returns ErrNoSingleValueTypeMeetsThreshold", func(t *testing.T) {
		t.Parallel()
		_, _, err := filterObservations(observationProtos, 3, true)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrNoSingleValueTypeMeetsThreshold), "got %v", err)
	})

	t.Run("migration disabled returns ErrNoValuesMetThreshold", func(t *testing.T) {
		t.Parallel()
		_, _, err := filterObservations(observationProtos, 3, false)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrNoValuesMetThreshold), "got %v", err)
	})
}
