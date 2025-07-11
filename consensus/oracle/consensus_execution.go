package oracle

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

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
// It now supports median aggregation for Int64, Float64, Decimal, BigInt, and Time types. It assumes that the observationProtos
// are already validated to ensure they all correctly unmarshal to a values.Value
func CalculateOutcomeForObservations(
	observationProtos []*valuespb.Value,
	consensusDescriptor *pb.ConsensusDescriptor,
	minObservations int,
	f int,
) (*valuespb.Value, error) {
	filtered, consensusType, err := filterObservations(observationProtos, minObservations)
	if err != nil {
		return nil, err
	}

	switch desc := consensusDescriptor.GetDescriptor_().(type) {
	case *pb.ConsensusDescriptor_Aggregation:
		aggregation := consensusDescriptor.GetAggregation()
		switch aggregation {
		case pb.AggregationType_AGGREGATION_TYPE_IDENTICAL:
			return handleIdenticalAggregation(filtered, f)
		case pb.AggregationType_AGGREGATION_TYPE_MEDIAN:
			return handleMedianAggregation(filtered, consensusType)
		case pb.AggregationType_AGGREGATION_TYPE_COMMON_PREFIX:
			return handleCommonPrefixAggregation(filtered, f)
		case pb.AggregationType_AGGREGATION_TYPE_COMMON_SUFFIX:
			return handleCommonSuffixAggregation(filtered, f)
		default:
			return nil, fmt.Errorf("unknown aggregation type: %s", aggregation)
		}
	case *pb.ConsensusDescriptor_FieldsMap:
		// TODO: Implement aggregation for structured types (FieldsMap).
		return nil, errors.New("TODO only primitive aggregation types are supported right now")
	default:
		return nil, fmt.Errorf("unknown consensus descriptor type: %T", desc)
	}
}

func handleMedianAggregation(observations []*valuespb.Value, medianType string) (*valuespb.Value, error) {
	var (
		medianResult *valuespb.Value
		err          error
	)

	switch medianType {
	case TypeInt64:
		medianResult, err = getMedian(
			observations,
			func(val *valuespb.Value) (int64, error) {
				return val.GetInt64Value(), nil
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
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate int64 median: %w", err)
		}

	case TypeFloat64:
		medianResult, err = getMedian(
			observations,
			func(val *valuespb.Value) (float64, error) {
				return val.GetFloat64Value(), nil
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
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate float64 median: %w", err)
		}

	case TypeDecimal:
		medianResult, err = getMedian(
			observations,
			func(val *valuespb.Value) (decimal.Decimal, error) {
				var d decimal.Decimal
				v, err := values.FromProto(val)
				if err != nil {
					return d, err
				}
				return d, v.UnwrapTo(&d)
			},
			func(a, b decimal.Decimal) int {
				return a.Cmp(b)
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate decimal median: %w", err)
		}

	case TypeBigInt:
		medianResult, err = getMedian(
			observations,
			func(val *valuespb.Value) (*big.Int, error) {
				var got big.Int
				v, err := values.FromProto(val)
				if err != nil {
					return nil, err
				}
				return &got, v.UnwrapTo(&got)
			},
			func(a, b *big.Int) int {
				return a.Cmp(b)
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate big.Int median: %w", err)
		}

	case TypeTime:
		medianResult, err = getMedian(
			observations,
			func(val *valuespb.Value) (time.Time, error) {
				var got time.Time
				v, err := values.FromProto(val)
				if err != nil {
					return got, err
				}
				return got, v.UnwrapTo(&got)
			},
			func(a, b time.Time) int {
				return a.Compare(b)
			},
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate time median: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported type for median aggregation: %s", medianType)
	}

	return medianResult, nil
}

func handleIdenticalAggregation(values []*valuespb.Value, f int) (*valuespb.Value, error) {
	n := len(values)
	if n == 0 {
		return nil, errors.New("input slice cannot be empty for identical aggregation")
	}

	type valueOccurrence struct {
		count int
		value *valuespb.Value
	}

	var (
		marshaler       = &proto.MarshalOptions{Deterministic: true}
		identityMap     = make(map[string]valueOccurrence)
		uniqueCandidate *valuespb.Value
	)

	for _, currentValue := range values {
		b, err := marshaler.Marshal(currentValue)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal value: %w", err)
		}
		key := string(b)

		observation := identityMap[key]
		observation.count++
		if observation.value == nil {
			observation.value = currentValue
		}
		identityMap[key] = observation

		if observation.count == f+1 {
			if uniqueCandidate != nil {
				return nil, errors.New("not identical, multiple values with f+1 occurrences")
			}
			uniqueCandidate = observation.value
		}
	}

	if uniqueCandidate == nil {
		return nil, errors.New("no values met f+1 threshold")
	}

	return uniqueCandidate, nil
}

func handleCommonSuffixAggregation(_ []*valuespb.Value, _ int) (*valuespb.Value, error) {
	// TODO: Implement common suffix aggregation logic.
	return nil, fmt.Errorf("common suffix aggregation type not supported")
}

func handleCommonPrefixAggregation(_ []*valuespb.Value, _ int) (*valuespb.Value, error) {
	// TODO: Implement common prefix aggregation logic.
	return nil, fmt.Errorf("common prefix aggregation type not supported")
}

// countTypes takes a slice of valuespb.Value and returns a map
// where keys are the constant string names of the corresponding values.Value types
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

// filterObservations returns all the observations that meet the minimum observation
// threshold of the same underlying type.  Errors if no single type meets the
// threshold.
func filterObservations(observationProtos []*valuespb.Value, minObservations int) ([]*valuespb.Value, string, error) {
	if len(observationProtos) < minObservations {
		return nil, "", fmt.Errorf("insufficient observations (%d) to meet minimum (%d)", len(observationProtos), minObservations)
	}

	typeCounts := countTypes(observationProtos)

	var dominantType string
	var maxCount int
	for typeName, count := range typeCounts {
		if count >= minObservations {
			if count > maxCount {
				maxCount = count
				dominantType = typeName
			}
		}
	}

	if dominantType == "" || dominantType == TypeNil {
		return nil, "", fmt.Errorf("no single type met the minimum observation threshold of %d", minObservations)
	}

	observations := make([]*valuespb.Value, 0, len(observationProtos))
	for _, obsProto := range observationProtos {
		obs, err := values.FromProto(obsProto)
		if err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal observation value: %w", err)
		}
		if reflect.TypeOf(obs).String() == dominantType {
			observations = append(observations, obsProto)
		}
	}

	return observations, dominantType, nil
}

// getMedian is a generic helper function that calculates the median
// for a slice of values.Value that can be unwrapped to type T.
// It accepts functions for unwrapping and comparing the values.
//
// For an even number of elements, we take the left of the two middle elements.
func getMedian[T any](
	observations []*valuespb.Value,
	unwrap func(val *valuespb.Value) (T, error),
	compare func(a, b T) int,
) (*valuespb.Value, error) {
	if len(observations) < 1 {
		return nil, errors.New("no valid observations for median calculation")
	}

	var unwrappedValues []T
	for _, v := range observations {
		unwrapped, err := unwrap(v)
		if err != nil {
			return nil, err
		}
		unwrappedValues = append(unwrappedValues, unwrapped)
	}

	slices.SortFunc(unwrappedValues, compare)

	medianVal := unwrappedValues[len(unwrappedValues)/2]
	if len(unwrappedValues)%2 == 0 && len(unwrappedValues) > 0 {
		medianVal = unwrappedValues[len(unwrappedValues)/2-1]
	}

	v, err := values.Wrap(medianVal)
	if err != nil {
		return nil, err
	}

	return values.Proto(v), nil
}
