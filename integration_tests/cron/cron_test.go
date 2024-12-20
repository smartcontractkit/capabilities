package crontest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/logger"

	"github.com/smartcontractkit/capabilities/integration_tests/utils"
)

const (
	scheduleEverySecond = "* * * * * *"
)

type Payload struct {
	// Time that cron trigger's task execution occurred (RFC3339Nano formatted)
	ActualExecutionTime string `json:"ActualExecutionTime" yaml:"ActualExecutionTime" mapstructure:"ActualExecutionTime"`

	// Time that cron trigger's task execution had been scheduled to occur
	// (RFC3339Nano formatted)
	ScheduledExecutionTime string `json:"ScheduledExecutionTime" yaml:"ScheduledExecutionTime" mapstructure:"ScheduledExecutionTime"`
}

func Test_CronTrigger(t *testing.T) {
	ctx, cancel := framework.Context(t)
	defer cancel()
	lggr := logger.TestLogger(t)
	lggr.SetLogLevel(zapcore.InfoLevel)
	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	cronBinary, err := utils.DeployCapability(t, "cron")
	require.NoError(t, err)

	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "Workflow", NumNodes: 7, F: 2, AcceptsWorkflows: true})
	require.NoError(t, err)

	targetSink := framework.NewTargetSink("mock-target", "1.0.0")

	setupCronTestDon(ctx, t, lggr, workflowDonConfiguration, scheduleEverySecond, targetSink, cronBinary, 1)

	quorum := 3 // number of nodes that need to execute the workflow (F+1)
	runs := 3   // number of rounds to be considered done
	waitTime := 60 * time.Second

	waitFor(ctx, t, targetSink, quorum, runs, waitTime)
}

func waitFor(ctx context.Context, t *testing.T, targetSink *framework.TargetSink, quorum int, expectedNumRuns int, waitTime time.Duration) {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, waitTime)
	defer cancel()

	hasQuorum := 0
	countCalls := 0
	idsToActualTime := map[string][]time.Time{}

	for {
		select {
		case <-ctxWithTimeout.Done():
			t.Fatalf("timed out waiting for runs, expected %d, received %d", expectedNumRuns, hasQuorum)
		case request := <-targetSink.Sink:
			assert.NotNil(t, request.Inputs)

			const field = "data"
			data := request.Inputs.Underlying[field]
			var payload Payload
			err := data.UnwrapTo(&payload)
			assert.NoError(t, err)

			countCalls++

			actualTime, _ := time.Parse(time.RFC3339Nano, payload.ActualExecutionTime)
			idsToActualTime[request.Metadata.WorkflowExecutionID] = append(idsToActualTime[request.Metadata.WorkflowExecutionID], actualTime)

			// Check that the actual execution time of trigger is within a second across nodes
			if len(idsToActualTime[request.Metadata.WorkflowExecutionID]) > 1 {
				for i, executionTime := range idsToActualTime[request.Metadata.WorkflowExecutionID] {
					if i > 0 {
						assert.True(t, executionTime.Before(idsToActualTime[request.Metadata.WorkflowExecutionID][0].Add(time.Second)))
					}
				}
			}

			if len(idsToActualTime[request.Metadata.WorkflowExecutionID]) == quorum {
				hasQuorum++
			}

			if hasQuorum == expectedNumRuns {
				return
			}
		}
	}
}
