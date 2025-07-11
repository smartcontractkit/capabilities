package oracle

import (
	"bytes"
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

// TypeOperations holds unwrap and comparison functions for a specific underlying Go type.
type TypeOperations struct {
	Unwrap  func(val values.Value) (any, error)
	Compare func(a, b any) int
}

type ConsensusHandler func(observations []values.Value) (values.Value, error)

type observationHandler struct {
	typeOperations    map[string]TypeOperations
	medianHandlers    map[string]ConsensusHandler
	identicalHandlers map[string]ConsensusHandler
}

func newObservationHandler() *observationHandler {
	// A map to store TypeOperations for each supported values.Value type.
	// Defined here for self reference in recursive comparison functions.
	var typeOperations map[string]TypeOperations
	typeOperations = map[string]TypeOperations{
		TypeInt64: {
			Unwrap: func(val values.Value) (any, error) {
				var got int64
				err := val.UnwrapTo(&got)
				return got, err
			},
			Compare: func(a, b any) int {
				valA, okA := a.(int64)
				valB, okB := b.(int64)
				if !okA || !okB {
					return 0
				}
				if valA < valB {
					return -1
				}
				if valA > valB {
					return 1
				}
				return 0
			},
		},
		TypeFloat64: {
			Unwrap: func(val values.Value) (any, error) {
				var got float64
				err := val.UnwrapTo(&got)
				return got, err
			},
			Compare: func(a, b any) int {
				valA, okA := a.(float64)
				valB, okB := b.(float64)
				if !okA || !okB {
					return 0
				}
				if valA < valB {
					return -1
				}
				if valA > valB {
					return 1
				}
				return 0
			},
		},
		TypeDecimal: {
			Unwrap: func(val values.Value) (any, error) {
				var got decimal.Decimal
				err := val.UnwrapTo(&got)
				return got, err
			},
			Compare: func(a, b any) int {
				valA, okA := a.(decimal.Decimal)
				valB, okB := b.(decimal.Decimal)
				if !okA || !okB {
					return 0
				}
				return valA.Cmp(valB)
			},
		},
		TypeBigInt: {
			Unwrap: func(val values.Value) (any, error) {
				got := new(big.Int)
				err := val.UnwrapTo(got)
				return got, err
			},
			Compare: func(a, b any) int {
				valA, okA := a.(*big.Int)
				valB, okB := b.(*big.Int)
				if !okA || !okB {
					return 0
				}
				return valA.Cmp(valB)
			},
		},
		TypeTime: {
			Unwrap: func(val values.Value) (any, error) {
				var got time.Time
				err := val.UnwrapTo(&got)
				return got, err
			},
			Compare: func(a, b any) int {
				valA, okA := a.(time.Time)
				valB, okB := b.(time.Time)
				if !okA || !okB {
					return 0
				}
				return valA.Compare(valB)
			},
		},
		TypeString: {
			Unwrap: func(val values.Value) (any, error) {
				var got string
				err := val.UnwrapTo(&got)
				return got, err
			},
			Compare: func(a, b any) int {
				valA, okA := a.(string)
				valB, okB := b.(string)
				if !okA || !okB {
					return 0
				}
				if valA < valB {
					return -1
				}
				if valA > valB {
					return 1
				}
				return 0
			},
		},
		TypeBool: {
			Unwrap: func(val values.Value) (any, error) {
				var got bool
				err := val.UnwrapTo(&got)
				return got, err
			},
			Compare: func(a, b any) int {
				valA, okA := a.(bool)
				valB, okB := b.(bool)
				if !okA || !okB {
					return 0
				}
				if !valA && valB {
					return -1
				}
				if valA && !valB {
					return 1
				}
				return 0
			},
		},
		TypeBytes: {
			Unwrap: func(val values.Value) (any, error) {
				var got []byte
				err := val.UnwrapTo(&got)
				return got, err
			},
			Compare: func(a, b any) int {
				valA, okA := a.([]byte)
				valB, okB := b.([]byte)
				if !okA || !okB {
					return 0
				}
				return bytes.Compare(valA, valB)
			},
		},
		TypeMap: {
			Unwrap: func(v values.Value) (any, error) {
				var got map[string]any
				err := v.UnwrapTo(&got)
				return got, err
			},
			Compare: func(a, b any) int {
				mapA, okA := a.(map[string]any)
				mapB, okB := b.(map[string]any)

				// Handle nil maps after successful type assertion
				if mapA == nil && mapB == nil {
					return 0
				}
				if mapA == nil || mapB == nil {
					return -1
				}

				if !okA || !okB {
					return -1
				}

				if len(mapA) != len(mapB) {
					return -1
				}

				// Get sorted keys for consistent iteration order
				keysA := make([]string, 0, len(mapA))
				for k := range mapA {
					keysA = append(keysA, k)
				}
				slices.Sort(keysA)

				for _, key := range keysA {
					valA := mapA[key]
					valB, ok := mapB[key]
					if !ok {
						return -1 // Key in A but not in B
					}

					// Recursively compare using compareAnyValue
					if compareAnyValue(valA, valB, typeOperations) != 0 {
						return -1
					}
				}
				return 0
			},
		},
		TypeList: {
			Unwrap: func(v values.Value) (any, error) { // Unwrap via values.FromProto for complex types
				var got []any
				err := v.UnwrapTo(&got)
				return got, err
			},
			Compare: func(a, b any) int {
				listA, okA := a.([]any)
				listB, okB := b.([]any)

				// Handle nil slices after successful type assertion
				if listA == nil && listB == nil {
					return 0
				}
				if listA == nil || listB == nil {
					return -1 // One is nil, other is not
				}

				if !okA || !okB {
					return -1 // Type mismatch or not lists
				}

				if len(listA) != len(listB) {
					return -1 // Different lengths
				}

				for i := range listA {
					valA := listA[i]
					valB := listB[i]

					// Recursively compare using compareAnyValue
					if compareAnyValue(valA, valB, typeOperations) != 0 {
						return -1
					}
				}
				return 0
			},
		},
	}

	// A map of functions to handle median aggregation for specific types.
	medianTypeHandlers := map[string]ConsensusHandler{
		TypeInt64: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeInt64]
			return getMedian(observations, ops.Unwrap, ops.Compare)
		},
		TypeFloat64: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeFloat64]
			return getMedian(observations, ops.Unwrap, ops.Compare)
		},
		TypeDecimal: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeDecimal]
			return getMedian(observations, ops.Unwrap, ops.Compare)
		},
		TypeBigInt: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeBigInt]
			return getMedian(observations, ops.Unwrap, ops.Compare)
		},
		TypeTime: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeTime]
			return getMedian(observations, ops.Unwrap, ops.Compare)
		},
	}

	// A map of functions to handle identical aggregation for specific types.
	identicalTypeHandlers := map[string]ConsensusHandler{
		TypeInt64: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeInt64]
			return getIdentical(observations, ops.Unwrap, ops.Compare)
		},
		TypeFloat64: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeFloat64]
			return getIdentical(observations, ops.Unwrap, ops.Compare)
		},
		TypeDecimal: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeDecimal]
			return getIdentical(observations, ops.Unwrap, ops.Compare)
		},
		TypeBigInt: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeBigInt]
			return getIdentical(observations, ops.Unwrap, ops.Compare)
		},
		TypeTime: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeTime]
			return getIdentical(observations, ops.Unwrap, ops.Compare)
		},
		TypeString: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeString]
			return getIdentical(observations, ops.Unwrap, ops.Compare)
		},
		TypeBool: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeBool]
			return getIdentical(observations, ops.Unwrap, ops.Compare)
		},
		TypeBytes: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeBytes]
			return getIdentical(observations, ops.Unwrap, ops.Compare)
		},
		TypeMap: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeMap]
			return getIdentical(observations, ops.Unwrap, ops.Compare)
		},
		TypeList: func(observations []values.Value) (values.Value, error) {
			ops := typeOperations[TypeList]
			return getIdentical(observations, ops.Unwrap, ops.Compare)
		},
	}

	return &observationHandler{
		typeOperations:    typeOperations,
		medianHandlers:    medianTypeHandlers,
		identicalHandlers: identicalTypeHandlers,
	}
}

// CalculateOutcomeForObservations determines the outcome for a set of observations based on a consensus descriptor.
// It now supports median aggregation for Int64, Float64, Decimal, BigInt, and Time types.
func (h *observationHandler) CalculateOutcomeForObservations(
	observationProtos []*valuespb.Value,
	consensusDescriptor *pb.ConsensusDescriptor,
	minObservations int,
) (*valuespb.Value, error) {
	// Use filterObservations to determine the final selected type and get the pre-filtered observations
	observations, dominantType, err := filterObservations(observationProtos, minObservations)
	if err != nil {
		return nil, err
	}

	var outcomeValue *valuespb.Value

	switch desc := consensusDescriptor.GetDescriptor_().(type) {
	case *pb.ConsensusDescriptor_Aggregation:
		aggregation := consensusDescriptor.GetAggregation()
		switch aggregation {
		case pb.AggregationType_AGGREGATION_TYPE_IDENTICAL:
			outcomeValue, err = h.handleIdenticalAggregation(observations, dominantType)
		case pb.AggregationType_AGGREGATION_TYPE_MEDIAN:
			outcomeValue, err = h.handleMedianAggregation(observations, dominantType)
		case pb.AggregationType_AGGREGATION_TYPE_COMMON_PREFIX:
			outcomeValue, err = h.handleCommonPrefixAggregation(observations, dominantType)
		case pb.AggregationType_AGGREGATION_TYPE_COMMON_SUFFIX:
			outcomeValue, err = h.handleCommonSuffixAggregation(observations, dominantType)
		default:
			return nil, fmt.Errorf("unknown aggregation type: %s", aggregation)
		}
	case *pb.ConsensusDescriptor_FieldsMap:
		// TODO: Implement aggregation for structured types (FieldsMap).
		return nil, errors.New("TODO only primitive aggregation types are supported right now")
	default:
		return nil, fmt.Errorf("unknown consensus descriptor type: %T", desc)
	}

	if err != nil {
		return nil, err
	}
	return outcomeValue, nil
}

// handleMedianAggregation calculates the median of observations for the determined dominant type.
// It expects `observations` to already contain only values of `dominantType`.
func (h *observationHandler) handleMedianAggregation(observations []values.Value, dominantType string) (*valuespb.Value, error) {
	handler, ok := h.medianHandlers[dominantType]
	if !ok {
		return nil, fmt.Errorf("unsupported type for median aggregation: %s", dominantType)
	}
	medianResult, err := handler(observations)
	if err != nil {
		return nil, err
	}
	return values.Proto(medianResult), nil
}

// handleIdenticalAggregation checks if all filtered observations are identical and returns the common value.
func (h *observationHandler) handleIdenticalAggregation(observations []values.Value, dominantType string) (*valuespb.Value, error) {
	handler, ok := h.identicalHandlers[dominantType]
	if !ok {
		return nil, fmt.Errorf("unsupported type for identical aggregation: %s", dominantType)
	}
	identicalResult, err := handler(observations)
	if err != nil {
		return nil, err
	}
	return values.Proto(identicalResult), nil
}

func (h *observationHandler) handleCommonSuffixAggregation(_ []values.Value, _ string) (*valuespb.Value, error) {
	// TODO: Implement common suffix aggregation logic.
	// This handler should identify the longest common suffix among string or bytes observations and return it.
	return nil, fmt.Errorf("common suffix aggregation type not supported")
}

func (h *observationHandler) handleCommonPrefixAggregation(_ []values.Value, _ string) (*valuespb.Value, error) {
	// TODO: Implement common prefix aggregation logic.
	// This handler should identify the longest common prefix among string or bytes observations and return it.
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
func getMedian(
	observations []values.Value,
	unwrap func(val values.Value) (any, error),
	compare func(a, b any) int,
) (values.Value, error) {
	if len(observations) < 1 {
		return nil, errors.New("no valid observations for median calculation")
	}

	var unwrappedValues []any
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

// getIdentical ensures all observations are identical via the compare function and returns the first value.
func getIdentical(
	observations []values.Value,
	unwrap func(val values.Value) (any, error),
	compare func(a, b any) int,
) (values.Value, error) {
	if len(observations) < 1 {
		return nil, errors.New("no observations to determine identical value")
	}

	firstUnwrapped, err := unwrap(observations[0])
	if err != nil {
		return nil, fmt.Errorf("failed to unwrap first observation for identical check: %w", err)
	}

	for i := 1; i < len(observations); i++ {
		currentUnwrapped, err := unwrap(observations[i])
		if err != nil {
			return nil, fmt.Errorf("failed to unwrap observation %d for identical check: %w", i, err)
		}
		if compare(firstUnwrapped, currentUnwrapped) != 0 {
			return nil, fmt.Errorf("observations are not identical: mismatch found at index %d", i)
		}
	}

	return values.Wrap(firstUnwrapped)
}

func compareAnyValue(a, b any, typeOperations map[string]TypeOperations) int {
	// Handle nil cases first
	if a == nil && b == nil {
		return 0
	}
	if a == nil || b == nil {
		return -1 // One is nil, other is not
	}

	typeOfA := reflect.TypeOf(a)
	typeOfB := reflect.TypeOf(b)

	// If concrete types are different, they are generally not comparable in our system.
	if typeOfA != typeOfB {
		return -1
	}

	// Determine the TypeOperations based on the concrete Go type of 'a'.
	var opsForVal TypeOperations
	var ok bool

	switch typeOfA {
	case reflect.TypeOf(int64(0)):
		opsForVal, ok = typeOperations[TypeInt64]
	case reflect.TypeOf(float64(0)):
		opsForVal, ok = typeOperations[TypeFloat64]
	case reflect.TypeOf(decimal.Decimal{}):
		opsForVal, ok = typeOperations[TypeDecimal]
	case reflect.TypeOf(&big.Int{}):
		opsForVal, ok = typeOperations[TypeBigInt]
	case reflect.TypeOf(time.Time{}):
		opsForVal, ok = typeOperations[TypeTime]
	case reflect.TypeOf(""):
		opsForVal, ok = typeOperations[TypeString]
	case reflect.TypeOf(true):
		opsForVal, ok = typeOperations[TypeBool]
	case reflect.TypeOf([]byte{}):
		opsForVal, ok = typeOperations[TypeBytes]
	case reflect.TypeOf(map[string]any{}): // Recursive case for map[string]any
		opsForVal, ok = typeOperations[TypeMap]
	case reflect.TypeOf([]any{}): // Recursive case for []any (from values.List)
		opsForVal, ok = typeOperations[TypeList]
	default:
		// Default do not handled an unlisted type
		return -1
	}

	if ok {
		return opsForVal.Compare(a, b)
	}

	return -1
}
