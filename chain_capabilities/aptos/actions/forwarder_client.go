package actions

import (
	"context"
	"fmt"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	"github.com/aptos-labs/aptos-go-sdk/bcs"
	aptos_forwarder "github.com/smartcontractkit/chainlink-aptos/bindings/platform/forwarder"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

// CREForwarderClient abstracts the interaction with the Aptos CRE forwarder contract.
type CREForwarderClient interface {
	// InvokeOnReport builds and submits a forwarder report transaction to the Aptos chain.
	InvokeOnReport(ctx context.Context, receiver [32]byte, report *sdk.ReportResponse, gasConfig *aptoscap.GasConfig) (*aptostypes.SubmitTransactionReply, error)
}

type forwarderClient struct {
	types.AptosService
	lggr             logger.Logger
	forwarderAddress [32]byte
	forwarderEncoder aptos_forwarder.ForwarderEncoder
}

func newForwarderClient(aptosService types.AptosService, lggr logger.Logger, forwarderAddress [32]byte) CREForwarderClient {
	emptyClient := aptos_sdk.Client{}
	forwarder := aptos_forwarder.NewForwarder(forwarderAddress, &emptyClient)
	forwarderEncoder := forwarder.Encoder()
	return &forwarderClient{
		AptosService:     aptosService,
		lggr:             lggr,
		forwarderAddress: forwarderAddress,
		forwarderEncoder: forwarderEncoder,
	}
}

func (fc *forwarderClient) InvokeOnReport(ctx context.Context, receiver [32]byte, report *sdk.ReportResponse, gasConfig *aptoscap.GasConfig) (*aptostypes.SubmitTransactionReply, error) {

	// get transmitter address somehow
	// use receiver address
	// use report.RawReport
	// this thing here is what we came to consensus on
	// this has the client payload wrapped inside it and a bunch of other stuff
	// the forwarder contract is responsible for unwrapping the client payload and forwarding it to the receiver
	// use report.sigs
	// encode that as a report call on the forwarder contract

	// Build the payload for the forwarder's report entry function.
	// Format: [num_sigs (1 byte)] + [signatures...] + [raw_report] + [report_context]

	var signatures [][]byte
	for _, sig := range report.Sigs {
		signatures = append(signatures, sig.Signature)
	}

	receiverAddress := aptos_sdk.AccountAddress(receiver)
	moduleInformation, _, argTypes, args, err := fc.forwarderEncoder.Report(receiverAddress, report.RawReport, signatures)
	if err != nil {
		return nil, fmt.Errorf("failed to encode forwarder report: %w", err)
	}

	payload := aptos_sdk.TransactionPayload{
		Payload: &aptos_sdk.EntryFunction{
			Module: aptos_sdk.ModuleId{
				Address: moduleInformation.Address,
				Name:    moduleInformation.ModuleName,
			},
			Function: "report",
			ArgTypes: argTypes,
			Args:     args,
		},
	}
	encodedPayload, err := bcs.Serialize(&payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal forwarder report payload: %w", err)
	}

	var resolvedGasConfig *aptostypes.GasConfig
	if gasConfig != nil {
		resolvedGasConfig = &aptostypes.GasConfig{
			MaxGasAmount: gasConfig.MaxGasAmount,
			GasUnitPrice: gasConfig.GasUnitPrice,
		}
	}

	reply, err := fc.AptosService.SubmitTransaction(ctx, aptostypes.SubmitTransactionRequest{
		ReceiverModuleID: aptostypes.ModuleID{
			Address: aptostypes.AccountAddress(fc.forwarderAddress),
			Name:    moduleInformation.ModuleName,
		},
		EncodedPayload: encodedPayload,
		GasConfig:      resolvedGasConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to submit forwarder report transaction: %w", err)
	}

	return reply, nil
}

// buildForwarderPayload builds the payload bytes sent to the Aptos forwarder.
// Format: [num_sigs (1 byte)] + [signatures...] + [raw_report] + [report_context]
// This mirrors the Solana forwarder payload format.
func buildForwarderPayload(report *sdk.ReportResponse) []byte {
	var payload []byte

	// 1. Number of signatures
	payload = append(payload, byte(len(report.Sigs)))

	// 2. Signatures
	for _, sig := range report.Sigs {
		payload = append(payload, sig.Signature...)
	}

	// 3. Raw report
	payload = append(payload, report.RawReport...)

	// 4. Report context
	payload = append(payload, report.ReportContext...)

	return payload
}
