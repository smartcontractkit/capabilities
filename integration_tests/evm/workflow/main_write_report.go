//go:build wasip1

package main

import (
	"crypto/rand"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/wasm/v2"
)

func RunSimpleCronWorkflow(_ *sdk.Environment[struct{}]) (sdk.Workflow[struct{}], error) {
	// TODO lautaro: WIP example to validate stuff, will be deleted later
	cfg := &cron.Config{
		Schedule: "*/3 * * * * *", // every 3 seconds
	}

	return sdk.Workflow[struct{}]{
		sdk.Handler(
			cron.Trigger(cfg),
			onTrigger,
		),
	}, nil
}

func onTrigger(env *sdk.Environment[struct{}], runtime sdk.Runtime, outputs *cron.Payload) (string, error) {
	fmt.Println(">>> Bravo")
	// env.Logger.Error("Workflow executed bravo!!!!")
	evmClient := evm.Client{}
	fmt.Println(">>> Before WriteReport")
	replyPromise := evmClient.WriteReport(runtime, &evm.WriteReportRequest{Receiver: GenerateRandomBytes(40), Report: createReport()})
	reply, err := replyPromise.Await()
	fmt.Println(">>> TxStatus: " + reply.TxStatus.String())
	// fmt.Println(">>> ReceiverStatus: " + reply.ReceiverContractExecutionStatus.String())
	fmt.Println(">>> TxHash: " + common.Bytes2Hex(reply.TxHash))
	var message = "empty message"
	if reply.ErrorMessage != nil {
		message = *reply.ErrorMessage
	}
	fmt.Println(">>> ErrorMessage: " + message)
	if err != nil {
		fmt.Println(">>> Error in WriteReport", err)
	} else {
		fmt.Println(">>> WriteReport reply:", reply)
	}
	return "ping", nil
}

func createReport() *evm.SignedReport {
	return &evm.SignedReport{
		RawReport:     GenerateRandomBytes(20),
		ReportContext: GenerateRandomBytes(20),
		Signatures: [][]byte{
			{1, 2, 3, 4},
		},
		Id: GenerateRandomBytes(2),
	}
}

func main() {
	// wasm.NewRunner(func(_ []byte) (struct{}, error) { return struct{}{}, nil }).Run(RunSimpleCronWorkflow)
	wasm.NewRunner(func(_ []byte) (struct{}, error) { return struct{}{}, nil }).Run(RunSimpleCronWorkflow)
}

// GenerateRandomBytes returns a slice of securely generated random bytes.
// It will return an error if the system's secure random number generator fails.
func GenerateRandomBytes(length int) []byte {
	bytes := make([]byte, length)
	rand.Read(bytes)
	return bytes
}
