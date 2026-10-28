package gasmeter

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
)

// faultSettings builds a getter with PerOrg.BillingFault* overrides for org.
func faultSettings(t *testing.T, org string, kv map[string]string) settings.Getter {
	t.Helper()
	cfg := fmt.Sprintf("[org.%s.PerOrg]\n", org)
	for k, v := range kv {
		cfg += fmt.Sprintf("%s = %q\n", k, v)
	}
	g, err := settings.NewTOMLGetter([]byte(cfg))
	require.NoError(t, err)
	return g
}

// Fault injection is read from CRE settings by the shared ResourceManager, so
// it reaches the gas meter with no code here.
func TestEmit_FaultInjection(t *testing.T) {
	metadata := capabilities.RequestMetadata{WorkflowID: "wf-1", WorkflowExecutionID: "exec-1", OrgID: "org-1"}

	t.Run("perturb subtracts delta from the emitted fee", func(t *testing.T) {
		emitter := &recordingEmitter{}
		cfg := enabledCfg(emitter)
		cfg.Settings = faultSettings(t, "org-1", map[string]string{"BillingFaultPerturbRate": "1", "BillingFaultPerturbDelta": "-7"})
		m := New(logger.Test(t), cfg, 1, 7)
		m.Emit(t.Context(), metadata, "0xabc", big.NewInt(100))
		require.Len(t, emitter.records, 1)
		require.Equal(t, "93", emitter.records[0].GetUtilizations()[0].GetValue())
	})

	t.Run("drop emits nothing", func(t *testing.T) {
		emitter := &recordingEmitter{}
		cfg := enabledCfg(emitter)
		cfg.Settings = faultSettings(t, "org-1", map[string]string{"BillingFaultDropRate": "1"})
		m := New(logger.Test(t), cfg, 1, 7)
		m.Emit(t.Context(), metadata, "0xabc", big.NewInt(100))
		require.Empty(t, emitter.records)
	})

	t.Run("other org is untouched", func(t *testing.T) {
		emitter := &recordingEmitter{}
		cfg := enabledCfg(emitter)
		cfg.Settings = faultSettings(t, "org-canary", map[string]string{"BillingFaultDropRate": "1"})
		m := New(logger.Test(t), cfg, 1, 7)
		m.Emit(t.Context(), metadata, "0xabc", big.NewInt(100))
		require.Len(t, emitter.records, 1)
		require.Equal(t, "100", emitter.records[0].GetUtilizations()[0].GetValue())
	})
}
