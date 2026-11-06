package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/capabilities/internal/consensus/protos"
	"github.com/smartcontractkit/capabilities/internal/consensus/protos/protostest"
	"github.com/smartcontractkit/capabilities/libs/capability/capabilitytest"
)

// TestNew runs a DON of embedded instances, each with its own oracle, and checks they agree on a median.
func TestNew(t *testing.T) {
	caps := capabilitytest.Start(t, New, 4)

	info, err := caps[0].Info(t.Context())
	require.NoError(t, err)
	assert.Equal(t, protos.ConsensusID, info.ID)

	input := &sdk.SimpleConsensusInputs{
		Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(values.NewInt64(10))},
		Descriptors: &sdk.ConsensusDescriptor{
			Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN},
		},
	}

	// A request an instance gets before its oracle has a config expires at once, so ask until the DON is up.
	var execution int
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		execution++
		metadata := capabilities.RequestMetadata{
			WorkflowID:               "0039525c34de895c8fa68006bd63f6ce4a45ef1bc66377e791c6a8ae803dc0e4",
			WorkflowOwner:            "1139525c34de895c8fa68006bd634387a9f1192a",
			WorkflowExecutionID:      fmt.Sprintf("%064x", execution),
			WorkflowName:             "a1b2c3d4e5f6a1b2c3d4",
			WorkflowDonID:            1,
			WorkflowDonConfigVersion: 1,
			ReferenceID:              "01",
		}

		result, err := protostest.ConsensusInstances(caps).Simple(t.Context(), metadata, input)
		if !assert.NoError(c, err) {
			return
		}

		assert.True(c, proto.Equal(values.Proto(values.NewInt64(10)), result.Response), "got %v", result.Response)
	}, 30*time.Second, 100*time.Millisecond)
}
