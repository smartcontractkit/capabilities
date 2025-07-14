package oracle

import (
	"bytes"
	"crypto/sha256"
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
// It now supports median aggregation for Int64, Float64, Decimal, BigInt, and Time types.
func CalculateOutcomeForObservations(
	observationProtos []*valuespb.Value,
	consensusDescriptor *pb.ConsensusDescriptor,
	minObservations int,
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
			return handleIdenticalAggregation(observationProtos)
		case pb.AggregationType_AGGREGATION_TYPE_MEDIAN:
			return handleMedianAggregation(filtered, consensusType)
		case pb.AggregationType_AGGREGATION_TYPE_COMMON_PREFIX:
			return handleCommonPrefixAggregation(filtered, consensusType)
		case pb.AggregationType_AGGREGATION_TYPE_COMMON_SUFFIX:
			return handleCommonSuffixAggregation(filtered, consensusType)
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

func handleMedianAggregation(observations []values.Value, medianType string) (*valuespb.Value, error) {
	var (
		medianResult values.Value
		err          error
	)

	switch medianType {
	case TypeInt64:
		medianResult, err = getMedian(
			observations,
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
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate int64 median: %w", err)
		}

	case TypeFloat64:
		medianResult, err = getMedian(
			observations,
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
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate float64 median: %w", err)
		}

	case TypeDecimal:
		medianResult, err = getMedian(
			observations,
			func(val values.Value) (decimal.Decimal, error) {
				var got decimal.Decimal
				return got, val.UnwrapTo(&got)
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
			func(val values.Value) (*big.Int, error) {
				got := new(big.Int)
				return got, val.UnwrapTo(got)
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
			func(val values.Value) (time.Time, error) {
				var got time.Time
				return got, val.UnwrapTo(&got)
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

	return values.Proto(medianResult), nil
}

func handleIdenticalAggregation(values []*valuespb.Value) (*valuespb.Value, error) {
	if len(values) == 0 {
		return nil, errors.New("input slice cannot be empty for identical aggregation")
	}

	// Use deterministic marshaling for consistent byte representation
	opts := proto.MarshalOptions{Deterministic: true}

	firstValueBytes, err := opts.Marshal(values[0])
	if err != nil {
		return nil, fmt.Errorf("failed to marshal first value: %w", err)
	}
	firstValueHash := sha256.Sum256(firstValueBytes)

	// Compare the first hash to the hash of each subsequent value
	for i := 1; i < len(values); i++ {
		currentValueBytes, err := opts.Marshal(values[i])
		if err != nil {
			return nil, fmt.Errorf("failed to marshal value at index %d: %w", i, err)
		}
		currentValueHash := sha256.Sum256(currentValueBytes)

		if !bytes.Equal(firstValueHash[:], currentValueHash[:]) {
			return nil, fmt.Errorf("values are not identical: mismatch found at index %d", i)
		}
	}

	return values[0], nil
}

func handleCommonSuffixAggregation(_ []values.Value, _ string) (*valuespb.Value, error) {
	// TODO: Implement common suffix aggregation logic.
	return nil, fmt.Errorf("common suffix aggregation type not supported")
}

func handleCommonPrefixAggregation(_ []values.Value, _ string) (*valuespb.Value, error) {
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
func filterObservations(observationProtos []*valuespb.Value, minObservations int) ([]values.Value, string, error) {
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

	observations := make([]values.Value, 0, len(observationProtos))
	for _, obsProto := range observationProtos {
		obs, err := values.FromProto(obsProto)
		if err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal observation value: %w", err)
		}
		if reflect.TypeOf(obs).String() == dominantType {
			observations = append(observations, obs)
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
	observations []values.Value,
	unwrap func(val values.Value) (T, error),
	compare func(a, b T) int,
) (values.Value, error) {
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

	return values.Wrap(medianVal)
}
