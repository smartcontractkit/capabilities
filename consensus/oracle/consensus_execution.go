package oracle

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"time"

	"github.com/shopspring/decimal"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"
)

// Constants for type names used in aggregation logic.
const (
	TypeInt64   = "*values.Int64"
	TypeFloat64 = "*values.Float64"
	TypeDecimal = "*values.Decimal"
	TypeBigInt  = "*values.BigInt"
	TypeString  = "*values.String"
	TypeBool    = "*values.Bool"
	TypeBytes   = "*values.Bytes"
	TypeMap     = "*values.Map"
	TypeList    = "*values.List"
	TypeTime    = "*values.Time"
	TypeNil     = "<nil>" // Represents a nil values.Value or protobuf value
)

// CalculateOutcomeForObservations determines the outcome for a set of observations based on a consensus descriptor.
// It now supports median aggregation for Int64, Float64, Decimal, BigInt, and Time types.
func CalculateOutcomeForObservations(
	observationProtos []*valuespb.Value,
	consensusDescriptor *pb.ConsensusDescriptor,
	minObservations int,
) (*valuespb.Value, error) {
	if len(observationProtos) < minObservations {
		return nil, fmt.Errorf("insufficient observations (%d) to meet minimum (%d)", len(observationProtos), minObservations)
	}

	finalSelectedTypeName, err := determineFinalSelectedType(observationProtos, minObservations)
	if err != nil {
		return nil, err
	}

	observations := make([]values.Value, len(observationProtos))
	for i, obsProto := range observationProtos {
		obs, err := values.FromProto(obsProto)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal observation value: %w", err)
		}
		observations[i] = obs
	}

	var outcomeValue *valuespb.Value

	switch desc := consensusDescriptor.GetDescriptor_().(type) {
	case *pb.ConsensusDescriptor_Aggregation:
		aggregation := consensusDescriptor.GetAggregation()
		switch aggregation {
		case pb.AggregationType_AGGREGATION_TYPE_IDENTICAL:
			outcomeValue, err = handleIdenticalAggregation(observations, finalSelectedTypeName)
		case pb.AggregationType_AGGREGATION_TYPE_MEDIAN:
			outcomeValue, err = handleMedianAggregation(observations, finalSelectedTypeName)
		case pb.AggregationType_AGGREGATION_TYPE_COMMON_PREFIX:
			outcomeValue, err = handleCommonPrefixAggregation(observations, finalSelectedTypeName)
		case pb.AggregationType_AGGREGATION_TYPE_COMMON_SUFFIX:
			outcomeValue, err = handleCommonSuffixAggregation(observations, finalSelectedTypeName)
		default:
			return nil, fmt.Errorf("unknown aggregation type: %s", aggregation)
		}
	case *pb.ConsensusDescriptor_FieldsMap:
		// TODO: Implement aggregation for structured types (FieldsMap).
		// This handler needs to support consensus calculation for complex data structures defined by a FieldsMap.
		return nil, errors.New("TODO only primitive aggregation types are supported right now")
	default:
		return nil, fmt.Errorf("unknown consensus descriptor type: %T", desc)
	}

	if err != nil {
		return nil, err
	}
	return outcomeValue, nil
}

func handleMedianAggregation(observations []values.Value, finalSelectedTypeName string) (*valuespb.Value, error) {
	var filteredObservations []values.Value
	for _, v := range observations {
		if reflect.TypeOf(v).String() == finalSelectedTypeName {
			filteredObservations = append(filteredObservations, v)
		}
	}

	var (
		medianResult values.Value
		err          error
	)

	switch finalSelectedTypeName {
	case TypeInt64:
		medianResult, err = getMedianFromFilteredObservations(
			filteredObservations,
			func(val values.Value) (int64, error) {
				var got int64
				return got, val.UnwrapTo(&got)
			},
			func(a, b int64) int {
				if a < b {
					return -1
				}
				if a > b {
					return 1
				}
				return 0
			},
			values.Wrap,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate int64 median: %w", err)
		}

	case TypeFloat64:
		medianResult, err = getMedianFromFilteredObservations(
			filteredObservations,
			func(val values.Value) (float64, error) {
				var got float64
				return got, val.UnwrapTo(&got)
			},
			func(a, b float64) int {
				if a < b {
					return -1
				}
				if a > b {
					return 1
				}
				return 0
			},
			values.Wrap,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate float64 median: %w", err)
		}

	case TypeDecimal:
		medianResult, err = getMedianFromFilteredObservations(
			filteredObservations,
			func(val values.Value) (decimal.Decimal, error) {
				var got decimal.Decimal
				return got, val.UnwrapTo(&got)
			},
			func(a, b decimal.Decimal) int {
				return a.Cmp(b)
			},
			values.Wrap,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate decimal median: %w", err)
		}

	case TypeBigInt:
		medianResult, err = getMedianFromFilteredObservations(
			filteredObservations,
			func(val values.Value) (*big.Int, error) {
				got := new(big.Int)
				return got, val.UnwrapTo(got)
			},
			func(a, b *big.Int) int {
				return a.Cmp(b)
			},
			values.Wrap,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate big.Int median: %w", err)
		}

	case TypeTime:
		medianResult, err = getMedianFromFilteredObservations(
			filteredObservations,
			func(val values.Value) (time.Time, error) {
				var got time.Time
				return got, val.UnwrapTo(&got)
			},
			func(a, b time.Time) int {
				return a.Compare(b)
			},
			values.Wrap,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate time median: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported type for median aggregation: %s", finalSelectedTypeName)
	}

	return values.Proto(medianResult), nil
}

func handleIdenticalAggregation(_ []values.Value, _ string) (*valuespb.Value, error) {
	// TODO: Implement identical aggregation logic.
	// This handler should find if all valid observations are identical and return that value, or an error if not.
	return nil, fmt.Errorf("identical aggregation type not supported")
}

func handleCommonSuffixAggregation(_ []values.Value, _ string) (*valuespb.Value, error) {
	// TODO: Implement common suffix aggregation logic.
	// This handler should identify the longest common suffix among string or bytes observations and return it.
	return nil, fmt.Errorf("common suffix aggregation type not supported")
}

func handleCommonPrefixAggregation(_ []values.Value, _ string) (*valuespb.Value, error) {
	// TODO: Implement common prefix aggregation logic.
	// This handler should identify the longest common prefix among string or bytes observations and return it.
	return nil, fmt.Errorf("common prefix aggregation type not supported")
}

// countTypes takes a slice of valuespb.Value and returns a map
// where keys are the constant string names of the corresponding values.Value types (e.g., TypeInt64)
// and values are their counts.
func countTypes(observationProtos []*valuespb.Value) map[string]int {
	typeCounts := make(map[string]int)

	for _, obsProto := range observationProtos {
		if obsProto == nil || obsProto.Value == nil {
			typeCounts[TypeNil]++
			continue
		}

		var typeName string
		switch obsProto.Value.(type) {
		case *valuespb.Value_StringValue:
			typeName = TypeString
		case *valuespb.Value_BoolValue:
			typeName = TypeBool
		case *valuespb.Value_BytesValue:
			typeName = TypeBytes
		case *valuespb.Value_MapValue:
			typeName = TypeMap
		case *valuespb.Value_ListValue:
			typeName = TypeList
		case *valuespb.Value_DecimalValue:
			typeName = TypeDecimal
		case *valuespb.Value_Int64Value:
			typeName = TypeInt64
		case *valuespb.Value_BigintValue:
			typeName = TypeBigInt
		case *valuespb.Value_TimeValue:
			typeName = TypeTime
		case *valuespb.Value_Float64Value:
			typeName = TypeFloat64
		default:
			// Fallback for unknown or unhandled types (should be rare with a complete protobuf definition)
			typeName = fmt.Sprintf("unknown_proto_type_%T", obsProto.Value)
		}
		typeCounts[typeName]++
	}
	return typeCounts
}

// determineFinalSelectedType takes a slice of valuespb.Value and a minimum observation count,
// returning the constant string name of the most frequent values.Value type that meets the threshold, or an error.
// This version operates directly on protobufs.
func determineFinalSelectedType(observationProtos []*valuespb.Value, minObservations int) (string, error) {
	typeCounts := countTypes(observationProtos)

	var finalSelectedTypeName string
	var maxCount int
	for typeName, count := range typeCounts {
		if count >= minObservations {
			if count > maxCount {
				maxCount = count
				finalSelectedTypeName = typeName
			}
		}
	}

	if finalSelectedTypeName == "" || finalSelectedTypeName == TypeNil {
		return "", fmt.Errorf("no single type met the minimum observation threshold of %d", minObservations)
	}

	return finalSelectedTypeName, nil
}

// getMedianFromFilteredObservations is a generic helper function that calculates the median
// for a slice of values.Value that can be unwrapped to type T.
// It requires specific functions for unwrapping, comparing, and re-wrapping the values.
//
// For an even number of elements, we take the left of the two middle elements.
func getMedianFromFilteredObservations[T any](
	filteredObservations []values.Value,
	unwrap func(val values.Value) (T, error), // Unwraps values.Value to type T
	cmp func(a, b T) int, // Compares two values of type T
	wrap func(any) (values.Value, error), // Wraps type T back to values.Value
) (values.Value, error) {
	if len(filteredObservations) == 0 {
		return nil, errors.New("no valid observations for median calculation")
	}

	var unwrappedValues []T
	for _, v := range filteredObservations {
		unwrapped, err := unwrap(v)
		if err != nil {
			return nil, err
		}
		unwrappedValues = append(unwrappedValues, unwrapped)
	}

	slices.SortFunc(unwrappedValues, cmp)

	medianVal := unwrappedValues[len(unwrappedValues)/2]
	if len(unwrappedValues)%2 == 0 && len(unwrappedValues) > 0 {
		medianVal = unwrappedValues[len(unwrappedValues)/2-1]
	}

	return wrap(medianVal)
}
