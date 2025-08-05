package signertest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/confidential-compute/types"

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

	setupTestDon(ctx, t, lggr, workflowDonConfiguration, triggerSink, targetSink, binary)

	msg := []byte("test message")
	params, err := values.WrapMap(map[string]any{
		"requests": []map[string]interface{}{{
			"method":  "POST",
			"url":     "https://echo.com/post",
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
	var msgs string
	err = json.Unmarshal(responses[0].Body, &msgs)
	require.NoError(t, err)
	require.Equal(t, "peekaboo", msgs)
}
