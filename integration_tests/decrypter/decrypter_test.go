package decryptertest

import (
	cryptorand "crypto/rand"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/nacl/box"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/logger"

	"github.com/smartcontractkit/capabilities/integration_tests/utils"
)

func Test_Decrypter(t *testing.T) {
	ctx := t.Context()
	lggr := logger.TestLogger(t)
	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	decrypterBinary, err := utils.DeployCapability(t, "decrypter")
	require.NoError(t, err)

	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "Workflow", NumNodes: 4, F: 1, AcceptsWorkflows: true})
	require.NoError(t, err)

	triggerSink := framework.NewTriggerSink(t, "mock-trigger", "1.0.0")
	targetSink := framework.NewTargetSink("mock-target", "1.0.0")

	don := setupTestDon(ctx, t, lggr, workflowDonConfiguration, triggerSink, targetSink, decrypterBinary)
	workflowPubKeys := don.GetWorkflowPublicKeys()
	require.Len(t, workflowPubKeys, 4)

	msg := []byte("test message")
	var ciphertexts [][]byte
	for _, pubKey := range workflowPubKeys {
		ciphertext, err := box.SealAnonymous(nil, msg, pubKey, cryptorand.Reader)
		require.NoError(t, err)
		ciphertexts = append(ciphertexts, ciphertext)
	}
	actionParams, err := values.WrapMap(map[string]any{
		"DecryptInputs": map[string]any{
			"ciphertexts": ciphertexts,
		},
	})
	require.NoError(t, err)
	triggerSink.SendOutput(actionParams, uuid.New().String())

	readresult := <-targetSink.Sink
	require.NotNil(t, readresult)

	var accountID string
	err = readresult.Inputs.Underlying["accountID"].UnwrapTo(&accountID)
	require.NoError(t, err)
	require.Equal(t, core.StandardCapabilityAccount, accountID)

	var plaintext []byte
	fmt.Println("Out:", readresult.Inputs)
	err = readresult.Inputs.Underlying["plaintext"].UnwrapTo(&plaintext)
	require.NoError(t, err)
	require.Equal(t, msg, plaintext)
}
