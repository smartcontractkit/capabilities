//go:build wasip1

package main

import (
	"fmt"

	croncap "github.com/smartcontractkit/cre-sdk-go/capabilities/scheduler/cron"
	"github.com/smartcontractkit/cre-sdk-go/sdk"
	"github.com/smartcontractkit/cre-sdk-go/sdk/wasm"
)

func RunSimpleCronWorkflow(runner sdk.Runner[config]) {
	cfg := &croncap.Config{
		Schedule: "*/30 * * * * *", // every 30 seconds
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

	env.Logger.Info(fmt.Sprintf("V2 Workflow Execution Result: TEST system log triggered at time %s ", outputs.ScheduledExecutionTime))

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
