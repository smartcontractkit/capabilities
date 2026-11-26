package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

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

	result, err := protostest.ConsensusInstances(caps).Simple(t.Context(), newRequestMetaData(), median(values.Proto(values.NewInt64(10))))
	require.NoError(t, err)
	assert.True(t, proto.Equal(values.Proto(values.NewInt64(10)), result.Response), "got %v", result.Response)
}
