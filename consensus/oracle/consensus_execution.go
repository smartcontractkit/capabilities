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

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

var (
	ErrNoValuesMetThreshold       = errors.New("no values met f+1 threshold")
	ErrMultipleValuesMetThreshold = errors.New("not identical, multiple values with f+1 occurrences")
	ErrInsufficientObservations   = errors.New("insufficient observations to reach consensus")
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
	lggr logger.Logger,
	observations []*valuespb.Value,
	consensusDescriptor *sdk.ConsensusDescriptor,
	defaultValue *valuespb.Value,
	minObservations int,
	f int,
) (*valuespb.Value, error) {
	filtered, _, err := filterObservations(observations, minObservations)
	if err != nil {
		return nil, err
	}

	return handleDescriptor(lggr, consensusDescriptor, filtered, defaultValue, f)
}

func handleDescriptor(
	lggr logger.Logger,
	consensusDescriptor *sdk.ConsensusDescriptor,
	filtered []*valuespb.Value,
	defaultValue *valuespb.Value,
	f int,
) (*valuespb.Value, error) {
	switch desc := consensusDescriptor.GetDescriptor_().(type) {
	case *sdk.ConsensusDescriptor_Aggregation:
		aggregation := consensusDescriptor.GetAggregation()
		switch aggregation {
		case sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL:
			return handleIdenticalAggregation(lggr, filtered, f)
		case sdk.AggregationType_AGGREGATION_TYPE_MEDIAN:
			return handleMedianAggregation(lggr, filtered, f)
		case sdk.AggregationType_AGGREGATION_TYPE_COMMON_PREFIX:
			return handleCommonPrefixAggregation(lggr, filtered, f)
		case sdk.AggregationType_AGGREGATION_TYPE_COMMON_SUFFIX:
			return handleCommonSuffixAggregation(lggr, filtered, f)
		default:
			return nil, fmt.Errorf("unknown aggregation type: %s", aggregation)
		}
	case *sdk.ConsensusDescriptor_FieldsMap:
		return handleFieldsMapAggregation(lggr, filtered, desc.FieldsMap.GetFields(), defaultValue, f)
	default:
		return nil, fmt.Errorf("unknown consensus descriptor type: %T", desc)
	}
}

func handleFieldsMapAggregation(
	lggr logger.Logger,
	observations []*valuespb.Value,
	desc map[string]*sdk.ConsensusDescriptor,
	defaultValue *valuespb.Value,
	f int,
) (*valuespb.Value, error) {
	if len(observations) < f+1 {
		return nil, ErrInsufficientObservations
	}

	result := make(map[string]*valuespb.Value, 0)
	for key, d := range desc {
		var (
			aggregated *valuespb.Value
			err        error
			obsForKey  = make([]*valuespb.Value, 0, len(observations))
		)

		for i, obs := range observations {
			if obs != nil {
				switch obs.Value.(type) {
				case *valuespb.Value_MapValue:
					fields := obs.GetMapValue().GetFields()
					obsForKey = append(obsForKey, fields[key])
				default:
					lggr.Debugf("unsupported observation type at index %d for key %s: %T", i, key, obs.Value)
					continue
				}
			}
			lggr.Debugf("ignoring nil observation at index %d for key %s", i, key)
		}

		var defaultForKey *valuespb.Value
		if defaultValue != nil {
			switch defaultValue.Value.(type) {
			case *valuespb.Value_MapValue:
				defaultForKey = defaultValue.GetMapValue().GetFields()[key]
			default:
				lggr.Debugf("missing default for key: %s", key)
			}
		}

		aggregated, err = handleDescriptor(lggr, d, obsForKey, defaultForKey, f)
		if err == nil {
			result[key] = aggregated
			continue
		}

		if defaultForKey != nil {
			result[key] = defaultForKey
			continue
		}

		return nil, fmt.Errorf("aggregation for field failed '%s': %w", key, err)
	}

	return valuespb.NewMapValue(result), nil
}

// TODO: CAPPL-1029 handle mixed observations of uint64 that are encoded as Int64 and BigInt
func handleMedianAggregation(
	_ logger.Logger,
	observations []*valuespb.Value,
	f int,
) (*valuespb.Value, error) {
	var (
		medianResult *valuespb.Value
		err          error
	)

	// The Report function is guaranteed to receive at least 2f+1 distinct attributed
	// observations. By assumption, up to f of these may be faulty, which includes
	// being malformed. Conversely, there have to be at least f+1 valid observations.
	filtered, medianType, err := filterObservations(observations, f+1)
	if err != nil {
		return nil, err
	}

	switch medianType {
	case TypeInt64:
		medianResult, err = getMedian(
			filtered,
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
			f,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate int64 median: %w", err)
		}

	case TypeFloat64:
		medianResult, err = getMedian(
			filtered,
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
			f,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate float64 median: %w", err)
		}

	case TypeDecimal:
		medianResult, err = getMedian(
			filtered,
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
			f,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate decimal median: %w", err)
		}

	case TypeBigInt:
		medianResult, err = getMedian(
			filtered,
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
			f,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate big.Int median: %w", err)
		}

	case TypeTime:
		medianResult, err = getMedian(
			filtered,
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
			f,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate time median: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported type for median aggregation: %s", medianType)
	}

	return medianResult, nil
}

func handleIdenticalAggregation(_ logger.Logger, values []*valuespb.Value, f int) (*valuespb.Value, error) {
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
				return nil, ErrMultipleValuesMetThreshold
			}
			uniqueCandidate = observation.value
		}
	}

	if uniqueCandidate == nil {
		return nil, ErrNoValuesMetThreshold
	}

	return uniqueCandidate, nil
}

// handleCommonSuffixAggregation reverses the underlying lists in the slice of
// observations and delegates logic to handleCommonPrefixAggregation and then
// reverses the result a final time.
func handleCommonSuffixAggregation(lggr logger.Logger, observationSlices []*valuespb.Value, f int) (*valuespb.Value, error) {
	var reversedObservations []*valuespb.Value
	for i, obsProto := range observationSlices {
		reversed, err := reverseListValue(obsProto)
		if err != nil {
			lggr.Warnf("skipping observations at index %d: %s", i, err)
			continue
		}
		reversedObservations = append(reversedObservations, reversed)
	}

	commonPrefixOfReversed, err := handleCommonPrefixAggregation(lggr, reversedObservations, f)
	if err != nil {
		return nil, fmt.Errorf("failed to find common prefix of reversed lists: %w", err)
	}

	return reverseListValue(commonPrefixOfReversed)
}

func handleCommonPrefixAggregation(lggr logger.Logger, observations []*valuespb.Value, f int) (*valuespb.Value, error) {
	var lists []*valuespb.List
	var maxListLength int
	for i, obsProto := range observations {
		if obsProto != nil {
			switch obsProto.Value.(type) {
			case *valuespb.Value_ListValue:
				list := obsProto.GetListValue()
				lists = append(lists, list)
				if len(list.GetFields()) > maxListLength {
					maxListLength = len(list.GetFields())
				}
			default:
				lggr.Warnf("value at index %d is of type %T", i, obsProto.Value)
				continue
			}
		}
	}

	if len(lists) < f+1 {
		return nil, ErrInsufficientObservations
	}

	var commonPrefixElements []*valuespb.Value
	for i := range maxListLength {
		var elementsAtIndex []*valuespb.Value
		for _, list := range lists {
			if len(list.GetFields()) > i {
				elementsAtIndex = append(elementsAtIndex, list.GetFields()[i])
			}
		}

		identicalValue, err := handleIdenticalAggregation(lggr, elementsAtIndex, f)
		if err != nil {
			break
		}

		commonPrefixElements = append(commonPrefixElements, identicalValue)
	}

	return valuespb.NewListValue(commonPrefixElements), nil
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

		if obs == nil {
			continue
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
	f int,
) (*valuespb.Value, error) {
	if len(observations) < f+1 {
		return nil, ErrInsufficientObservations
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

func reverseListValue(list *valuespb.Value) (*valuespb.Value, error) {
	if list != nil {
		switch list.Value.(type) {
		case *valuespb.Value_ListValue:
			reversed := list.GetListValue().GetFields()
			reverse(reversed)
			return valuespb.NewListValue(reversed), nil
		default:
			return nil, fmt.Errorf("cannot reverse value of type %T", list.Value)
		}
	}
	return new(valuespb.Value), nil
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
