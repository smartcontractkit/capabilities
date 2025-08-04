package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/smartcontractkit/cre-sdk-go/capabilities/blockchain/evm"
	"github.com/smartcontractkit/cre-sdk-go/capabilities/scheduler/cron"
	"github.com/smartcontractkit/cre-sdk-go/cre"
	"github.com/smartcontractkit/cre-sdk-go/cre/wasm"

	chain_selectors "github.com/smartcontractkit/chain-selectors"

	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"

	workflowpb "github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"
)

func RunSimpleCronWorkflow(_ *cre.Environment[struct{}]) (cre.Workflow[struct{}], error) {
	// TODO lautaro: WIP example to validate stuff, will be deleted later
	cfg := &cron.Config{
		Schedule: "*/3 * * * * *", // every 3 seconds
	}

	return cre.Workflow[struct{}]{
		cre.Handler(
			cron.Trigger(cfg),
			onTrigger,
		),
	}, nil
}

func onTrigger(env *cre.Environment[struct{}], runtime cre.Runtime, outputs *cron.Payload) (string, error) {
	fmt.Println(">>> Bravo")
	// env.Logger.Error("Workflow executed bravo!!!!")
	evmClient := evm.Client{ChainSelector: chain_selectors.GETH_TESTNET.Selector}
	fmt.Println(">>> Before GenReport")

	payload := []byte("some_encoded_report_data")
	reportPromise := runtime.GenerateReport(&workflowpb.ReportRequest{
		EncodedPayload: payload,
		EncoderName:    "evm",
		SigningAlgo:    "ecdsa",
		HashingAlgo:    "keccak256",
	})
	fmt.Printf(">>> After GenReport %T ", runtime)

	fmt.Println(">>> B4 GenReport await")

	report, err := reportPromise.Await()
	if err != nil {
		return "", err
	}

	fmt.Println(">>> After GenReport await")

	fmt.Println(">>> report  is", report)

	replyPromise := evmClient.WriteReport(runtime, &evm.WriteReportRequest{
		Receiver: common.BytesToAddress(GenerateRandomBytes(40)).Bytes(),
		Report:   report,
	})
	fmt.Println(">>> Before after write report before promise ", replyPromise)

	reply, err := replyPromise.Await()
	if err != nil {
		fmt.Println(">>> Error in WriteReport", err)
		return "", err
	} else {
		fmt.Println(">>> WriteReport reply:", reply)
	}

	fmt.Println(">>> TxStatus: " + reply.TxStatus.String())
	// fmt.Println(">>> ReceiverStatus: " + reply.ReceiverContractExecutionStatus.String())
	fmt.Println(">>> TxHash: " + common.Bytes2Hex(reply.TxHash))
	var message = "empty message"
	if reply.ErrorMessage != nil {
		message = *reply.ErrorMessage
	}
	fmt.Println(">>> ErrorMessage: " + message)

	return "ping", nil
}

func createReport() (*workflowpb.ReportResponse, error) {
	executionID := hex.EncodeToString(GenerateRandomBytes(32))
	metadata := ocrtypes.Metadata{
		Version:          1,
		ExecutionID:      executionID,
		Timestamp:        1000,
		DONID:            10,
		DONConfigVersion: 2,
		WorkflowID:       hex.EncodeToString(GenerateRandomBytes(32)),
		WorkflowName:     hex.EncodeToString(GenerateRandomBytes(10)),
		WorkflowOwner:    hex.EncodeToString(GenerateRandomBytes(20)),
		ReportID:         hex.EncodeToString(GenerateRandomBytes(2)),
	}

	encoded, err := metadata.Encode()
	if err != nil {
		fmt.Println("Error encoding metadata:", err)
		return nil, err
	}

	return &workflowpb.ReportResponse{
		RawReport:     encoded,
		ReportContext: GenerateRandomBytes(20),
		Sigs: []*workflowpb.AttributedSignature{
			{Signature: []byte{0x01}},
			{Signature: []byte{0x02}},
			{Signature: []byte{0x03}},
			{Signature: []byte{0x04}},
		}}, nil
}

func main() {
	wasm.NewRunner(func(_ []byte) (struct{}, error) { return struct{}{}, nil }).Run(RunSimpleCronWorkflow)
}

// GenerateRandomBytes returns a slice of securely generated random bytes.
// It will return an error if the system's secure random number generator fails.
func GenerateRandomBytes(length int) []byte {
	bytes := make([]byte, length)
	rand.Read(bytes)
	return bytes
}
