package actions

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"slices"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	"github.com/aptos-labs/aptos-go-sdk/bcs"
	"github.com/ethereum/go-ethereum/common"
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
	// GetTransmitterTransactions returns transactions for the given transmitter address.
	// Pass nil for start/limit to get the latest page with default size.
	GetTransmitterTransactions(ctx context.Context, transmitter aptos_sdk.AccountAddress, start *uint64, limit *uint64) ([]*aptostypes.Transaction, error)
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
		lggr:             logger.Named(lggr, "ForwarderClient"),
		forwarderAddress: forwarderAddress,
		forwarderEncoder: forwarderEncoder,
	}
}

func (fc *forwarderClient) InvokeOnReport(ctx context.Context, receiver []byte, report *sdk.ReportResponse, gasConfig *aptoscap.GasConfig) (*aptostypes.SubmitTransactionReply, error) {
	fc.lggr.Debugw("InvokeOnReport called",
		"receiverLen", len(receiver),
		"reportContextLen", len(report.ReportContext),
		"rawReportLen", len(report.RawReport),
		"numSigs", len(report.Sigs),
		"hasGasConfig", gasConfig != nil,
	)

	var signatures [][]byte
	for _, sig := range report.Sigs {
		signatures = append(signatures, sig.Signature)
	}

	fullRawReport := slices.Concat(report.ReportContext, report.RawReport)

	receiverAddress := aptos_sdk.AccountAddress(receiver)
	moduleInformation, _, argTypes, args, err := fc.forwarderEncoder.Report(receiverAddress, fullRawReport, signatures)
	if err != nil {
		fc.lggr.Errorw("failed to encode forwarder report", "error", err)
		return nil, fmt.Errorf("failed to encode forwarder report: %w", err)
	}
	fc.lggr.Debugw("forwarder report encoded",
		"moduleAddress", moduleInformation.Address.String(),
		"moduleName", moduleInformation.ModuleName,
		"numArgTypes", len(argTypes),
		"numArgs", len(args),
	)

	payload := aptos_sdk.TransactionPayload{
		Payload: &aptos_sdk.EntryFunction{
			Module: aptos_sdk.ModuleId{
				Address: moduleInformation.Address, // this is the forwarder contract address
				Name:    moduleInformation.ModuleName,
			},
			Function: "report",
			ArgTypes: argTypes,
			Args:     args,
		},
	}
	encodedPayload, err := bcs.Serialize(&payload)
	if err != nil {
		fc.lggr.Errorw("failed to marshal forwarder report payload", "error", err)
		return nil, fmt.Errorf("failed to marshal forwarder report payload: %w", err)
	}
	fc.lggr.Debugw("payload BCS-serialized", "encodedPayloadLen", len(encodedPayload))

	var resolvedGasConfig *aptostypes.GasConfig
	if gasConfig != nil {
		resolvedGasConfig = &aptostypes.GasConfig{
			MaxGasAmount: gasConfig.MaxGasAmount,
			GasUnitPrice: gasConfig.GasUnitPrice,
		}
		fc.lggr.Debugw("gas config resolved", "maxGasAmount", gasConfig.MaxGasAmount, "gasUnitPrice", gasConfig.GasUnitPrice)
	}

	fc.lggr.Debugw("submitting transaction to AptosService",
		"forwarderAddress", fmt.Sprintf("%x", fc.forwarderAddress),
		"moduleName", moduleInformation.ModuleName,
	)
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
		fc.lggr.Errorw("SubmitTransaction failed", "error", err)
		return nil, fmt.Errorf("failed to submit forwarder report transaction: %w", err)
	}

	fc.lggr.Debugw("SubmitTransaction succeeded", "txHash", reply.TxHash, "txStatus", reply.TxStatus)
	return reply, nil
}

type TransmissionID struct {
	Receiver            aptos_sdk.AccountAddress
	ReportID            [2]byte
	WorkflowExecutionID [32]byte
}

func (t TransmissionID) GetDebugID() string {
	return fmt.Sprintf("receiver: %s, reportID: %s, workflowExecutionID %s", t.Receiver.String(), common.Bytes2Hex(t.ReportID[:]), common.Bytes2Hex(t.WorkflowExecutionID[:]))
}

type TransmissionInfo struct {
	Success     bool
	Transmitter aptos_sdk.AccountAddress
}

type moveOptionAddress struct {
	Vec []string `json:"vec"`
}

// Views GetTransmitter onchain
func (fc *forwarderClient) GetTransmissionInfo(ctx context.Context, transmissionID TransmissionID) (TransmissionInfo, error) {
	// Convert [2]byte report ID to uint16 (big-endian, as stored in report metadata)
	reportID := binary.BigEndian.Uint16(transmissionID.ReportID[:])

	fc.lggr.Debugw("GetTransmissionInfo called",
		"transmissionID", transmissionID.GetDebugID(),
		"receiverHex", fmt.Sprintf("%x", transmissionID.Receiver[:]),
		"workflowExecutionIDHex", fmt.Sprintf("%x", transmissionID.WorkflowExecutionID[:]),
		"reportIDBytes", fmt.Sprintf("%x", transmissionID.ReportID[:]),
		"reportIDUint16", reportID,
	)

	// Use the encoder to get the BCS-encoded view call arguments
	moduleInfo, functionName, _, args, err := fc.forwarderEncoder.GetTransmitter(
		transmissionID.Receiver,
		transmissionID.WorkflowExecutionID[:],
		reportID,
	)
	if err != nil {
		fc.lggr.Errorw("failed to encode GetTransmitter", "error", err)
		return TransmissionInfo{}, fmt.Errorf("failed to encode GetTransmitter: %w", err)
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
		fc.lggr.Errorw("GetTransmissionState view call failed", "error", err)
		return TransmissionInfo{}, fmt.Errorf("failed to call GetTransmissionState view: %w", err)
	}

	fc.lggr.Debugw("GetTransmissionState view returned", "rawData", string(viewReply.Data))

	// Move Option<T> is struct { vec: vector<T> }, so the Aptos REST API serializes it as:
	//   some(addr) → [{"vec": ["0xaddr"]}]
	//   none       → [{"vec": []}]
	var result []moveOptionAddress
	if err := json.Unmarshal(viewReply.Data, &result); err != nil {
		fc.lggr.Errorw("failed to unmarshal transmission state", "rawData", string(viewReply.Data), "error", err)
		return TransmissionInfo{}, fmt.Errorf("failed to unmarshal transmission state: %w", err)
	}

	if len(result) == 0 || len(result[0].Vec) == 0 {
		fc.lggr.Debugw("no transmitter found (not yet transmitted)")
		return TransmissionInfo{Success: false}, nil
	}

	var addr aptos_sdk.AccountAddress
	if err := addr.ParseStringRelaxed(result[0].Vec[0]); err != nil {
		fc.lggr.Errorw("failed to parse transmitter address", "raw", result[0].Vec[0], "error", err)
		return TransmissionInfo{}, fmt.Errorf("failed to parse transmitter address: %w", err)
	}

	fc.lggr.Debugw("transmission found", "transmitter", addr.String(), "success", true)
	return TransmissionInfo{Transmitter: addr, Success: true}, nil
}

func (fc *forwarderClient) GetTransmitterTransactions(ctx context.Context, transmitter aptos_sdk.AccountAddress, start *uint64, limit *uint64) ([]*aptostypes.Transaction, error) {
	fc.lggr.Debugw("GetTransmitterTransactions called",
		"transmitter", transmitter.String(),
		"hasStart", start != nil,
		"hasLimit", limit != nil,
	)
	reply, err := fc.AptosService.AccountTransactions(ctx, aptostypes.AccountTransactionsRequest{
		Address: aptostypes.AccountAddress(transmitter),
		Start:   start,
		Limit:   limit,
	})
	if err != nil {
		fc.lggr.Errorw("GetTransmitterTransactions failed", "transmitter", transmitter.String(), "error", err)
		return nil, fmt.Errorf("failed to get account transactions for transmitter %s: %w", transmitter.String(), err)
	}
	fc.lggr.Debugw("GetTransmitterTransactions returned", "transmitter", transmitter.String(), "txCount", len(reply.Transactions))
	return reply.Transactions, nil
}
