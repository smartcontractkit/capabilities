package signertest

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

const workflowTemplate = `
name: "%s"
owner: "0x%s"
triggers:
  - id: "mock-trigger@1.0.0"
    ref: "trigger"
    config:
       mustputavaluehere_thisisabug: "true"

actions:
  - id: "p2psigner-action@1.0.0"
    ref: "action2"
    inputs:
      $(trigger.outputs)
    config:
      mustputavaluehere_thisisabug: "true"

targets:
  - id: "mock-target@1.0.0"
    ref: "target"
    inputs:
      $(action2.outputs)
    config:
      mustputavaluehere_thisisabug: "true"
`

func GetWorkflowJob(
	t *testing.T,
	workflowName string,
	workflowOwner string,
) job.Job {
	workflowJobSpec := testspecs.GenerateWorkflowJobSpec(
		t,
		fmt.Sprintf(
			workflowTemplate,
			workflowName,
			workflowOwner,
		),
	)

	return workflowJobSpec.Job()
}
