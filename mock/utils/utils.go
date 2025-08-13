package utils

import (
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	pb2 "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/capabilities/mock/internal/pb"
)

func MapToBytes(m *values.Map) ([]byte, error) {
	if m == nil {
		return nil, nil
	}

	pm := make(map[string]*pb2.Value)
	for k, v := range m.Underlying {
		pm[k] = values.Proto(v)
	}
	bytes, err := proto.Marshal(pb2.NewMapValue(pm))
	if err != nil {
		return nil, err
	}
	return bytes, nil
}
func BytesToMap(b []byte) (*values.Map, error) {
	var o pb2.Value
	if err := proto.Unmarshal(b, &o); err != nil {
		return nil, err
	}

	vm := values.Map{Underlying: make(map[string]values.Value)}

	if o.Value == nil {
		return &vm, nil
	}

	for k, v := range o.GetMapValue().Fields {
		val, err := values.FromProto(v)
		if err != nil {
			return nil, err
		}
		vm.Underlying[k] = val
	}

	return &vm, nil
}

func ToMockServerEnum(c capabilities.CapabilityType) pb.CapabilityType {
	switch c {
	case capabilities.CapabilityTypeTrigger:
		return pb.CapabilityType_Trigger
	case capabilities.CapabilityTypeAction:
		return pb.CapabilityType_Action
	case capabilities.CapabilityTypeConsensus:
		return pb.CapabilityType_Consensus
	case capabilities.CapabilityTypeTarget:
		return pb.CapabilityType_Target
	default:
		return pb.CapabilityType_Unknown
	}
}

func ToCapabilityEnum(c pb.CapabilityType) capabilities.CapabilityType {
	switch c {
	case pb.CapabilityType_Trigger:
		return capabilities.CapabilityTypeTrigger
	case pb.CapabilityType_Action:
		return capabilities.CapabilityTypeAction
	case pb.CapabilityType_Consensus:
		return capabilities.CapabilityTypeConsensus
	case pb.CapabilityType_Target:
		return capabilities.CapabilityTypeTarget
	default:
		return capabilities.CapabilityTypeUnknown
	}
}
