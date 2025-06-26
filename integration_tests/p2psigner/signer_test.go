package signertest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"

	"github.com/smartcontractkit/capabilities/integration_tests/utils"
)

func Test_Signer(t *testing.T) {
	ctx := t.Context()
	lggr := logger.Test(t)
	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	signerBinary, err := utils.DeployCapability(t, "p2psigner")
	require.NoError(t, err)

	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "Workflow", NumNodes: 4, F: 1, AcceptsWorkflows: true})
	require.NoError(t, err)

	triggerSink := framework.NewTriggerSink(t, "mock-trigger", "1.0.0")
	targetSink := framework.NewTargetSink("mock-target", "1.0.0")

	don := setupTestDon(ctx, t, lggr, workflowDonConfiguration, triggerSink, targetSink, signerBinary)

	peers := don.GetPeerIDsAndOCRSigners()

	msg := []byte("test message")
	digest := sha256.Sum256(msg)
	signActionParams, err := values.WrapMap(map[string]any{
		"SignInputs": map[string]any{
			"digest": digest[:],
		},
	})
	require.NoError(t, err)
	triggerSink.SendOutput(signActionParams, uuid.New().String())

	readresult := <-targetSink.Sink
	require.NotNil(t, readresult)

	var accountID string
	err = readresult.Inputs.Underlying["accountID"].UnwrapTo(&accountID)
	require.NoError(t, err)
	require.Equal(t, "P2P_SIGNER", accountID)

	var sig []byte
	err = readresult.Inputs.Underlying["signature"].UnwrapTo(&sig)
	require.NoError(t, err)
	var valid bool
	for _, p := range peers {
		if ed25519.Verify(ed25519.PublicKey(p.PeerID[:]), digest[:], sig) {
			valid = true
			break
		}
	}
	require.True(t, valid)
}
