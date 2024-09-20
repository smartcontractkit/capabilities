package integration_tests

import (
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink/v2/core/services/standardcapabilities"
	"github.com/smartcontractkit/chainlink/v2/core/testdata/testspecs"

	"github.com/smartcontractkit/capabilities/integration_tests/shared/internal/cltest"
	"github.com/smartcontractkit/capabilities/integration_tests/shared/internal/testutils"
)

// ** TODO: keep workflow definitions in test file & pass through through to setup

const workflowTemplateCron = `
name: "%s"
owner: "0x%s"
triggers:
  - id: "cron-trigger@1.0.0"
    ref: "trigger"
    config:
      schedule:
        \"%s\"

targets:
  - id: "mock-target@1.0.0"
    ref: "target"
    inputs:
      data: $(trigger.outputs)
    config:
      deltaStage: %s
      schedule: %s
`

func addWorkflowJobCron(
	t *testing.T,
	app *cltest.TestApplication,
	workflowName string,
	workflowOwner string,
	cronSchedule string,
	consumerAddr common.Address,
	deltaStage string,
	schedule string,
) {
	workflowJobSpec := testspecs.GenerateWorkflowJobSpec(
		t,
		fmt.Sprintf(
			workflowTemplateCron,
			workflowName,
			workflowOwner,
			cronSchedule,
			deltaStage,
			schedule,
		),
	)

	job := workflowJobSpec.Job()

	err := app.AddJobV2(testutils.Context(t), &job)
	require.NoError(t, err)
}

// ** TODO: pull these from each individual capability e.g. cron/cron_capabilities_spec.toml
const standardCapabilityTemplateCron = `
type = "standardcapabilities"
schemaVersion = 1
name = "cron-capabilities"
command="../../bin/cron"
config=""
`

func addStandardCapabilityCron(
	t *testing.T,
	app *cltest.TestApplication,
) {
	job, err := standardcapabilities.ValidatedStandardCapabilitiesSpec(standardCapabilityTemplateCron)
	require.NoError(t, err)

	err = app.AddJobV2(testutils.Context(t), &job)
	require.NoError(t, err)
}

const standardCapabilityTemplateKeyValue = `
type = "standardcapabilities"
schemaVersion = 1
name = "kvstore-capabilities"
command="../../bin/kvstore"
config=""
`

func addStandardCapabilityKV(
	t *testing.T,
	app *cltest.TestApplication,
) {
	job, err := standardcapabilities.ValidatedStandardCapabilitiesSpec(standardCapabilityTemplateKeyValue)
	require.NoError(t, err)

	err = app.AddJobV2(testutils.Context(t), &job)
	require.NoError(t, err)
}
