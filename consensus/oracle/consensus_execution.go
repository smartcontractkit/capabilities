package oracle

import (
	"errors"
	"fmt"
	"slices"

	"github.com/smartcontractkit/chainlink-common/pkg/values"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"
)

// TODO hardcoded the aggregation to median of int64s for initial e2e test.
func CalculateOutcomeForObservations(observationProtos []*valuespb.Value, consensusDescriptor *pb.ConsensusDescriptor) (*valuespb.Value, error) {
	// TODO type check observations ahead of attempting aggregation, the type to use in comparison would
	// be that for which there are >= 2f+1 observations of the same type

	// TODO decide on what to do with errors - would they be aggregated as well in some way?  or just return error from the Outcome method

	observations := make([]values.Value, len(observationProtos))
	for i, obsProto := range observationProtos {
		obs, err := values.FromProto(obsProto)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal observation value: %w", err)
		}
		observations[i] = obs
	}

	consensusDescriptor.GetAggregation()

	switch desc := consensusDescriptor.GetDescriptor_().(type) {
	case *pb.ConsensusDescriptor_Aggregation:
		aggregation := consensusDescriptor.GetAggregation()
		switch aggregation {
		case pb.AggregationType_AGGREGATION_TYPE_IDENTICAL:
			return nil, fmt.Errorf("identical aggregation type not supported")
		case pb.AggregationType_AGGREGATION_TYPE_MEDIAN:
			var medianValues []int
			for _, v := range observations {
				var got int
				err := v.UnwrapTo(&got)
				if err != nil {
					return nil, fmt.Errorf("failed to unwrap value for median aggregation: %w", err)
				}

				medianValues = append(medianValues, got)
			}

			slices.Sort(medianValues)
			median := medianValues[len(medianValues)/2]
			return values.Proto(values.NewInt64(int64(median))), nil

		case pb.AggregationType_AGGREGATION_TYPE_COMMON_PREFIX:
			return nil, fmt.Errorf("common prefix aggregation type not supported")
		case pb.AggregationType_AGGREGATION_TYPE_COMMON_SUFFIX:
			return nil, fmt.Errorf("common suffix aggregation type not supported")
		default:
			return nil, fmt.Errorf("unknown aggregation type: %s", aggregation)
		}
	case *pb.ConsensusDescriptor_FieldsMap:
		// TODO support for structured types
		return nil, errors.New("TODO only primitive aggregation types are supported right now")
	default:
		return nil, fmt.Errorf("unknown consensus descriptor type: %T", desc)
	}
}
