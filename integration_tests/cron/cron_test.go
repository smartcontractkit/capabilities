package cron_integration_tests

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	shared "github.com/smartcontractkit/capabilities/integration_tests/shared"
	"github.com/smartcontractkit/capabilities/integration_tests/shared/mocks"
)

const (
	scheduleEveryOtherSecond = "*/2 * * * * *"
)

// TODO: import this type from "github.com/smartcontractkit/capabilities/cron/croncap"
type Payload struct {
	// Time that cron trigger's task execution occurred (RFC3339Nano formatted)
	ActualExecutionTime string `json:"ActualExecutionTime" yaml:"ActualExecutionTime" mapstructure:"ActualExecutionTime"`

	// Time that cron trigger's task execution had been scheduled to occur
	// (RFC3339Nano formatted)
	ScheduledExecutionTime string `json:"ScheduledExecutionTime" yaml:"ScheduledExecutionTime" mapstructure:"ScheduledExecutionTime"`
}

func Test_Cron_OneAtATimeTransmissionSchedule(t *testing.T) {
	ctx := shared.Context(t)

	// The don IDs set in the below calls are inferred from the order in which the dons are added to the capabilities registry
	// in the setupCapabilitiesRegistryContract function, should this order change the don IDs will need updating.
	workflowDonInfo := shared.CreateDonInfo(t, shared.Don{ID: 1, NumNodes: 7, F: 2})
	triggerDonInfo := shared.CreateDonInfo(t, shared.Don{ID: 2, NumNodes: 7, F: 2})
	targetDonInfo := shared.CreateDonInfo(t, shared.Don{ID: 3, NumNodes: 4, F: 1})

	_, _, targetSink := shared.SetupDonsWithTransmissionSchedule(ctx, t, workflowDonInfo, triggerDonInfo, targetDonInfo, scheduleEveryOtherSecond, "2s", "oneAtATime")

	quorum := 3 // number of nodes that need to execute the workflow (F+1)
	runs := 3   // number of rounds to reach quorum
	waitTime := 10 * time.Second

	waitFor(ctx, t, targetSink, quorum, runs, waitTime)
}

func waitFor(ctx context.Context, t *testing.T, targetSink *mocks.TargetSink, quorum int, expectedNumRuns int, waitTime time.Duration) {
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

			// Check that actual execution time of trigger is within a second across nodes
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
