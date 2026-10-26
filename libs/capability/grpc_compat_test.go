package capability

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

// TestGRPCServerMaxRecvMsgSizeRoundTripsThroughLoopEnv makes sure that this process reads the same
// env var as loop. The LOOP host sets the limit with loop.EnvConfig.AsCmdEnv. GRPCConfig must then
// contain the same limit.
func TestGRPCServerMaxRecvMsgSizeRoundTripsThroughLoopEnv(t *testing.T) {
	env := loop.EnvConfig{GRPCServerMaxRecvMsgSize: 7 << 20}
	for _, kv := range env.AsCmdEnv() {
		k, v, ok := strings.Cut(kv, "=")
		require.True(t, ok, "malformed env entry %q", kv)
		if strings.HasPrefix(k, "CL_GRPC_") {
			t.Setenv(k, v)
		}
	}

	assert.Equal(t, 7<<20, decoded(t).grpc.ServerMaxRecvMsgSize)
}
