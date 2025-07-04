//go:build wasip1

package main

import (
	"fmt"

	croncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/wasm/v2"
)

func RunSimpleCronWorkflow(runner sdk.Runner[config]) {
	cfg := &croncap.Config{
		Schedule: "*/2 * * * * *", // every 2 seconds
	}

	runner.Run(func(env *sdk.Environment[config]) (sdk.Workflow[config], error) {
		return sdk.Workflow[config]{
			sdk.Handler(
				croncap.Trigger(cfg),
				onTrigger),
		}, nil
	})
}

func onTrigger(env *sdk.Environment[config], runtime sdk.Runtime, outputs *croncap.Payload) (string, error) {

	var randomValue int64

	promise := sdk.RunInNodeMode[config, int64](env,
		runtime, func(env *sdk.NodeEnvironment[config], nrt sdk.NodeRuntime) (int64, error) {
			nr, err := nrt.Rand()
			if err != nil {
				return 0, err
			}

			randomValue = nr.Int63n(10)

			return randomValue, nil
		}, sdk.ConsensusMedianAggregation[int64]())

	consensusValue, err := promise.Await()
	if err != nil {
		return "", err
	}

	env.Logger.Info(fmt.Sprintf("V2 Workflow Execution Result: trigger time %s local value %d, consensus value %d", outputs.ScheduledExecutionTime, randomValue, consensusValue))

	return "complete", nil
}

type config struct {
}

func main() {
	runner := wasm.NewRunner[config](func(configBytes []byte) (config, error) {
		return config{}, nil
	})
	RunSimpleCronWorkflow(runner)
}
