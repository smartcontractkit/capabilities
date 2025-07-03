package crontest

import (
	"fmt"
	"testing"

	"github.com/smartcontractkit/chainlink/v2/core/services/job"
	"github.com/smartcontractkit/chainlink/v2/core/testdata/testspecs"
)

var (
	workflowName    = "abcdef0123"
	workflowOwnerID = "0100000000000000000000000000000000000001"
)

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
      mustputavaluehere_thisisabug: "true"
`

func GetWorkflowJobCron(
	t *testing.T,
	workflowName string,
	workflowOwner string,
	cronSchedule string,
) job.Job {
	workflowJobSpec := testspecs.GenerateWorkflowJobSpec(
		t,
		fmt.Sprintf(
			workflowTemplateCron,
			workflowName,
			workflowOwner,
			cronSchedule,
		),
	)

	return workflowJobSpec.Job()
}
