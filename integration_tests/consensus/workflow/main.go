//go:build wasip1

package main

import (
	"fmt"

	"github.com/smartcontractkit/cre-sdk-go/capabilities/scheduler/cron"
	"github.com/smartcontractkit/cre-sdk-go/sdk"
	"github.com/smartcontractkit/cre-sdk-go/sdk/wasm"
)

func RunSimpleCronWorkflow(_ *sdk.Environment[struct{}]) (sdk.Workflow[struct{}], error) {
	cfg := &cron.Config{
		Schedule: "*/2 * * * * *", // every 2 seconds
	}

	return sdk.Workflow[struct{}]{
		sdk.Handler(
			cron.Trigger(cfg),
			onTrigger,
		),
	}, nil
}

func onTrigger(env *sdk.Environment[struct{}], runtime sdk.Runtime, outputs *cron.Payload) (string, error) {

	var randomValue int64

	consensusValue, err := sdk.RunInNodeMode(env, runtime, func(env *sdk.NodeEnvironment[struct{}], nrt sdk.NodeRuntime) (int64, error) {
		nr, err := nrt.Rand()
		if err != nil {
			return 0, err
		}

		randomValue = nr.Int63n(10)

		return randomValue, nil
	}, sdk.ConsensusMedianAggregation[int64]()).Await()

	if err != nil {
		env.Logger.Error(fmt.Sprintf("Error in RunInNodeMode: %v", err))
	} else {
		env.Logger.Info(fmt.Sprintf("V2 Workflow Execution Result: trigger time %s local value %d, consensus value %d", outputs.ScheduledExecutionTime, randomValue, consensusValue))
	}

	return "complete", nil
}

func main() {
	wasm.NewRunner(func(_ []byte) (struct{}, error) { return struct{}{}, nil }).Run(RunSimpleCronWorkflow)
}
