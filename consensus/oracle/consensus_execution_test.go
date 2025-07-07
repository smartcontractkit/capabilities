package oracle

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/values"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"
)

func Test_CalculateOutcomeForObservations(t *testing.T) {
	observations := []*valuespb.Value{
		values.Proto(values.NewInt64(30)),
		values.Proto(values.NewInt64(40)),
		values.Proto(values.NewInt64(10)),
		values.Proto(values.NewInt64(20)),
		values.Proto(values.NewInt64(50)),
	}

	consensusDescriptor := &pb.ConsensusDescriptor{
		Descriptor_: &pb.ConsensusDescriptor_Aggregation{
			Aggregation: pb.AggregationType_AGGREGATION_TYPE_MEDIAN,
		},
	}

	expectedOutcome := values.Proto(values.NewInt64(30))

	outcome, err := CalculateOutcomeForObservations(observations, consensusDescriptor)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !proto.Equal(outcome, expectedOutcome) {
		t.Errorf("expected outcome %v, got %v", expectedOutcome, outcome)
	}
}
