package actions

import (
	"context"
	"encoding/binary"
	"encoding/json"
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
	InvokeOnReport(ctx context.Context, receiver []byte, report *sdk.ReportResponse, gasConfig *aptoscap.GasConfig) (*aptostypes.SubmitTransactionReply, error)
	// GetTransmissionInfo queries the forwarder contract for the transmission state of a given transmission ID.
	GetTransmissionInfo(ctx context.Context, transmissionID TransmissionID) (TransmissionInfo, error)
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

func (fc *forwarderClient) InvokeOnReport(ctx context.Context, receiver []byte, report *sdk.ReportResponse, gasConfig *aptoscap.GasConfig) (*aptostypes.SubmitTransactionReply, error) {
	// use receiver address
	// use report.RawReport
	// the report.RawReport is what we came to consensus on
	// the report.RawReport has the client payload wrapped inside it and a bunch of other stuff
	// the forwarder contract is responsible for unwrapping the client payload and forwarding it to the receiver
	// use report.sigs
	// encode that as a report call on the forwarder contract

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
		// TODO: do i really need ReceiverModuleID if my EncodedPayload is of type EntryFunction which has all the details ?
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

type TransmissionID struct {
	Receiver            aptos_sdk.AccountAddress
	ReportID            [2]byte
	WorkflowExecutionID [32]byte
}

type TransmissionInfo struct {
	Success bool
}

func (fc *forwarderClient) GetTransmissionInfo(ctx context.Context, transmissionID TransmissionID) (TransmissionInfo, error) {
	// Convert [2]byte report ID to uint16 (big-endian, as stored in report metadata)
	reportID := binary.BigEndian.Uint16(transmissionID.ReportID[:])

	// Use the encoder to get the BCS-encoded view call arguments
	moduleInfo, functionName, _, args, err := fc.forwarderEncoder.GetTransmissionState(
		transmissionID.Receiver,
		transmissionID.WorkflowExecutionID[:],
		reportID,
	)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to encode GetTransmissionState: %w", err)
	}

	// Call the view function via AptosService
	viewReply, err := fc.AptosService.View(ctx, aptostypes.ViewRequest{
		Payload: &aptostypes.ViewPayload{
			Module: aptostypes.ModuleID{
				Address: aptostypes.AccountAddress(moduleInfo.Address),
				Name:    moduleInfo.ModuleName,
			},
			Function: functionName,
			Args:     args,
		},
	})
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to call GetTransmissionState view: %w", err)
	}

	// Parse the JSON result — view returns a JSON array like [true] or [false]
	var result []bool
	if err := json.Unmarshal(viewReply.Data, &result); err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to unmarshal transmission state: %w", err)
	}
	if len(result) != 1 {
		return TransmissionInfo{}, fmt.Errorf("unexpected transmission state result length: %d", len(result))
	}

	return TransmissionInfo{Success: result[0]}, nil
}
