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

// Tests for the errors surfaced when nothing reaches the f+1 threshold, asserted on the
// exported sentinels rather than on message text.

func Test_CalculateOutcomeForObservations_identical_noValuesMetThreshold(t *testing.T) {
	t.Parallel()

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

	_, err := CalculateOutcomeForObservations(logger.Test(t), observations, desc, nil, 2, false)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoValuesMetFPlusOneThresholdForIdenticalConsensus),
		"expected ErrNoValuesMetFPlusOneThresholdForIdenticalConsensus, got %v", err)
}

func Test_filterObservations_noSingleValueTypeMeetsThreshold(t *testing.T) {
	t.Parallel()

	// Two int64 and two float64: with minObservations=3, no single type reaches the threshold.
	observationProtos := []*valuespb.Value{
		values.Proto(values.NewInt64(10)),
		values.Proto(values.NewInt64(20)),
		values.Proto(values.NewFloat64(1.0)),
		values.Proto(values.NewFloat64(2.0)),
	}

	_, _, err := filterObservations(observationProtos, 3)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoSingleValueTypeMeetsThreshold), "got %v", err)
}
