package main

import (
	"fmt"
	"log/slog"

	"github.com/smartcontractkit/cre-sdk-go/capabilities/scheduler/cron"
	"github.com/smartcontractkit/cre-sdk-go/cre"
	"github.com/smartcontractkit/cre-sdk-go/cre/wasm"
)

func RunSimpleCronWorkflow(
	_ struct{},
	_ *slog.Logger,
	_ cre.SecretsProvider,
) (cre.Workflow[struct{}], error) {
	cfg := &cron.Config{
		Schedule: "*/2 * * * * *", // every 2 seconds
	}

	return cre.Workflow[struct{}]{
		cre.Handler(
			cron.Trigger(cfg),
			onTrigger,
		),
	}, nil
}

func onTrigger(config struct{}, runtime cre.Runtime, outputs *cron.Payload) (string, error) {
	var randomValue int64

	consensusValue, err := cre.RunInNodeMode(config, runtime,
		func(_ struct{}, nrt cre.NodeRuntime) (int64, error) {
			nr, err := nrt.Rand()
			if err != nil {
				return 0, err
			}
			randomValue = nr.Int63n(10)
			return randomValue, nil
		},
		cre.ConsensusMedianAggregation[int64](),
	).Await()

	if err != nil {
		runtime.Logger().Error(fmt.Sprintf("Error in RunInNodeMode: %v", err))
	} else {
		runtime.Logger().Info(fmt.Sprintf(
			"V2 Workflow Execution Result: trigger time %s local value %d, consensus value %d",
			outputs.ScheduledExecutionTime, randomValue, consensusValue,
		))
	}

	return "complete", nil
}

func main() {
	wasm.NewRunner(func(_ []byte) (struct{}, error) { return struct{}{}, nil }).Run(RunSimpleCronWorkflow)
}
