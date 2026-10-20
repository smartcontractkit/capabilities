package gasmeter

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
)

// Fault injection is configured on the shared ResourceManagerConfig, so it
// reaches the gas meter with no code here.
func TestEmit_FaultInjection(t *testing.T) {
	metadata := capabilities.RequestMetadata{WorkflowID: "wf-1", WorkflowExecutionID: "exec-1", OrgID: "org-1"}

	t.Run("perturb adds delta to the emitted fee", func(t *testing.T) {
		emitter := &recordingEmitter{}
		cfg := enabledCfg(emitter)
		cfg.FaultInjection = resourcemanager.UsageFaultInjection{Mode: resourcemanager.FaultModePerturb, Rate: 1, Delta: 7}
		m := New(logger.Test(t), cfg, 1, 7)
		m.Emit(t.Context(), metadata, "0xabc", big.NewInt(100))
		require.Len(t, emitter.records, 1)
		require.Equal(t, "107", emitter.records[0].GetUtilizations()[0].GetValue())
	})

	t.Run("drop emits nothing", func(t *testing.T) {
		emitter := &recordingEmitter{}
		cfg := enabledCfg(emitter)
		cfg.FaultInjection = resourcemanager.UsageFaultInjection{Mode: resourcemanager.FaultModeDrop, Rate: 1}
		m := New(logger.Test(t), cfg, 1, 7)
		m.Emit(t.Context(), metadata, "0xabc", big.NewInt(100))
		require.Empty(t, emitter.records)
	})
}
