package signertest

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/confidential-compute/types"
	"github.com/smartcontractkit/confidential-compute/util"
	"github.com/smartcontractkit/tdh2/go/tdh2/tdh2easy"

	"github.com/smartcontractkit/capabilities/integration_tests/utils"
)

func Test_Confidential_HTTP_Action(t *testing.T) {
	ctx := t.Context()
	lggr := logger.Test(t)
	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	binary, err := utils.DeployCapability(t, "confidential_http_action")
	require.NoError(t, err)

	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "Workflow", NumNodes: 4, F: 1, AcceptsWorkflows: true})
	require.NoError(t, err)

	triggerSink := framework.NewTriggerSink(t, "mock-trigger", "1.0.0")
	targetSink := framework.NewTargetSink("mock-target", "1.0.0")

	_, publicKey, privateShares, err := tdh2easy.GenerateKeys(4, 4)
	require.NoError(t, err)

	url := "INSERT_URL_HERE"
	// url := "http://localhost:8081"
	don := setupTestDon(ctx, t, lggr, workflowDonConfiguration, triggerSink, targetSink, binary, publicKey, privateShares, url)

	peerIDsAndSigners := don.GetPeerIDsAndOCRSigners()
	masterPublicKeyBytes, err := publicKey.Marshal()
	require.NoError(t, err)

	var signers [][]byte
	for _, pIDAndS := range peerIDsAndSigners {
		signers = append(signers, pIDAndS.PeerID[:])
	}
	config := types.EnclaveConfig{
		Signers:         signers,
		MasterPublicKey: masterPublicKeyBytes,
		T:               4,
		F:               0,
	}
	configBytes, err := json.Marshal(config)
	require.NoError(t, err)
	req := types.ConfigRequest{
		Config: configBytes,
	}
	require.NoError(t, err)

	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // for demo purposes only.
			},
		},
	}
	resp, _ := util.SetNodeConfig(context.Background(), types.EnclaveNode{
		EnclaveURL:    url,
		EnclaveType:   types.EnclaveTypeNitro,
		TrustedValues: []byte{},
	}, req, &httpClient)
	fmt.Println(resp)

	msg := []byte("test message")
	params, err := values.WrapMap(map[string]any{
		"requests": []map[string]interface{}{{
			"method":  "POST",
			"url":     "https://postman-echo.com/post",
			"headers": []string{"Content-Type: application/json", "Authorization: Bearer {{.my_secret}}"},
			"body":    msg,
		}},
		"vaultDONSecrets": []map[string]interface{}{{
			"key": "my_secret",
		}},
	})
	require.NoError(t, err)
	triggerSink.SendOutput(params, uuid.New().String())

	readresult := <-targetSink.Sink
	require.NotNil(t, readresult)

	var responses []types.HTTPResponse
	err = readresult.Inputs.Underlying["responses"].UnwrapTo(&responses)
	require.NoError(t, err)
	require.Len(t, responses, 1)
	require.Equal(t, int64(http.StatusOK), responses[0].StatusCode)
	fmt.Println(string(responses[0].Body))
}
