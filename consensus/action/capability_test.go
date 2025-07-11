package action

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"

	"github.com/smartcontractkit/capabilities/libs/testutils"
)

// TODO tests for request limits, failed consensus, slow request and timeouts etc.

func TestCapability(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	capability := NewConsensusCapability(lggr, clockwork.NewRealClock(), time.Minute)

	oracleFactory := testutils.NewOracleFactory(t, lggr)

	err := capability.Initialise(ctx, "", nil, nil, nil, nil, nil,
		oracleFactory, nil, nil)
	require.NoError(t, err)

	servicetest.Run(t, capability)

	metadata := capabilities.RequestMetadata{
		WorkflowID:               "",
		WorkflowOwner:            "",
		WorkflowExecutionID:      "wex-id",
		WorkflowName:             "",
		WorkflowDonID:            0,
		WorkflowDonConfigVersion: 0,
		ReferenceID:              "1",
		DecodedWorkflowName:      "",
	}

	input := &pb.SimpleConsensusInputs{
		Observation: &pb.SimpleConsensusInputs_Value{
			Value: values.Proto(values.NewInt64(10)),
		},
		Descriptors: &pb.ConsensusDescriptor{
			Descriptor_: &pb.ConsensusDescriptor_Aggregation{
				Aggregation: pb.AggregationType_AGGREGATION_TYPE_MEDIAN,
			},
		},
		Default: nil,
	}

	result, err := capability.Simple(ctx, metadata, input)
	require.NoError(t, err)

	expectedResult, err := values.Wrap(10)
	require.NoError(t, err)
	expectedProto := values.Proto(expectedResult)

	require.True(t, proto.Equal(result, expectedProto))
}

func Test_SimpleInputsSizeValidation(t *testing.T) {
	lggr := logger.Test(t)
	ctx := t.Context()

	capability := NewConsensusCapability(lggr, clockwork.NewRealClock(), time.Minute)

	oracleFactory := testutils.NewOracleFactory(t, lggr)

	capConfig := &ConsensusCapabilityConfig{
		MaxRequestSizeBytes: 2,
	}

	capConfigJSON, err := json.Marshal(capConfig)
	require.NoError(t, err)

	err = capability.Initialise(ctx, string(capConfigJSON), nil, nil, nil, nil, nil,
		oracleFactory, nil, nil)
	require.NoError(t, err)

	servicetest.Run(t, capability)

	metadata := capabilities.RequestMetadata{
		WorkflowID:               "",
		WorkflowOwner:            "",
		WorkflowExecutionID:      "wex-id",
		WorkflowName:             "",
		WorkflowDonID:            0,
		WorkflowDonConfigVersion: 0,
		ReferenceID:              "1",
		DecodedWorkflowName:      "",
	}

	input := &pb.SimpleConsensusInputs{
		Observation: &pb.SimpleConsensusInputs_Value{Value: values.Proto(values.NewInt64(34))},
		Descriptors: &pb.ConsensusDescriptor{Descriptor_: &pb.ConsensusDescriptor_Aggregation{Aggregation: pb.AggregationType_AGGREGATION_TYPE_IDENTICAL}},
	}

	_, err = capability.Simple(ctx, metadata, input)
	require.Error(t, err)
	require.ErrorContains(t, err, "request size exceeds maximum allowed size")
}
