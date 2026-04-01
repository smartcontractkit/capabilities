package contracts

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"

	evmcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/forwarder"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

type TransmissionState uint8

const (
	TransmissionStateNotAttempted TransmissionState = iota
	TransmissionStateSucceeded
	TransmissionStateInvalidReceiver
	TransmissionStateFailed
)

func (s TransmissionState) String() string {
	switch s {
	case TransmissionStateNotAttempted:
		return "not_attempted"
	case TransmissionStateSucceeded:
		return "succeeded"
	case TransmissionStateInvalidReceiver:
		return "invalid_receiver"
	case TransmissionStateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

type ForwarderErrorType string

const (
	ForwarderErrorUnknown                      ForwarderErrorType = "unknown"
	ForwarderErrorAlreadyAttempted             ForwarderErrorType = "already_attempted"
	ForwarderErrorDuplicateSigner              ForwarderErrorType = "duplicate_signer"
	ForwarderErrorExcessSigners                ForwarderErrorType = "excess_signers"
	ForwarderErrorFaultToleranceMustBePositive ForwarderErrorType = "fault_tolerance_must_be_positive"
	ForwarderErrorInsufficientGasForRouting    ForwarderErrorType = "insufficient_gas_for_routing"
	ForwarderErrorInsufficientSigners          ForwarderErrorType = "insufficient_signers"
	ForwarderErrorInvalidConfig                ForwarderErrorType = "invalid_config"
	ForwarderErrorInvalidReport                ForwarderErrorType = "invalid_report"
	ForwarderErrorInvalidSignature             ForwarderErrorType = "invalid_signature"
	ForwarderErrorInvalidSignatureCount        ForwarderErrorType = "invalid_signature_count"
	ForwarderErrorInvalidSigner                ForwarderErrorType = "invalid_signer"
	ForwarderErrorUnauthorizedForwarder        ForwarderErrorType = "unauthorized_forwarder"
	ForwarderErrorStandardErrorString          ForwarderErrorType = "error_string"
	ForwarderErrorStandardPanic                ForwarderErrorType = "panic"
)

var forwarderErrorNameToType = map[string]ForwarderErrorType{
	"AlreadyAttempted":             ForwarderErrorAlreadyAttempted,
	"DuplicateSigner":              ForwarderErrorDuplicateSigner,
	"ExcessSigners":                ForwarderErrorExcessSigners,
	"FaultToleranceMustBePositive": ForwarderErrorFaultToleranceMustBePositive,
	"InsufficientGasForRouting":    ForwarderErrorInsufficientGasForRouting,
	"InsufficientSigners":          ForwarderErrorInsufficientSigners,
	"InvalidConfig":                ForwarderErrorInvalidConfig,
	"InvalidReport":                ForwarderErrorInvalidReport,
	"InvalidSignature":             ForwarderErrorInvalidSignature,
	"InvalidSignatureCount":        ForwarderErrorInvalidSignatureCount,
	"InvalidSigner":                ForwarderErrorInvalidSigner,
	"UnauthorizedForwarder":        ForwarderErrorUnauthorizedForwarder,
}

type TransmissionInfo struct {
	GasLimit        *big.Int          `json:"gasLimit,omitempty"`
	InvalidReceiver bool              `json:"invalidReceiver,omitempty"`
	State           TransmissionState `json:"state,omitempty"`
	Success         bool              `json:"success,omitempty"`
	//nolint:revive
	TransmissionId [32]byte       `json:"transmissionId,omitempty"`
	Transmitter    common.Address `json:"transmitter,omitempty"`
}

func (ti TransmissionInfo) LogAttrs() []any {
	attrs := make([]any, 0, 12)

	attrs = append(attrs,
		"transmissionState", ti.State,
		"transmissionSuccess", ti.Success,
		"invalidReceiver", ti.InvalidReceiver,
		"transmitter", ti.Transmitter.Hex(),
		"transmissionID", hex.EncodeToString(ti.TransmissionId[:]),
	)

	if ti.GasLimit != nil {
		attrs = append(attrs,
			"transmissionGasLimit", ti.GasLimit.String(),
		)
	} else {
		attrs = append(attrs,
			"transmissionGasLimit", (*big.Int)(nil),
		)
	}

	return attrs
}

// The gas cost of the forwarder contract logic, including state updates and event emission.
// This is a rough estimate and should be updated if the forwarder contract logic changes.
// PLEX-1524 - Make the forwarder contract logic gas cost limit configurable
const (
	// ForwarderContractLogicGasCost is at minimum 100k, but often goes up by several x*10%.
	// Overshoot it by double to make sure that we don't resend txs that had enough gas leftover based on transmission gas info.
	ForwarderContractLogicGasCost = 200_000
	LatestBlock                   = -2 // PLEX-1524 - Use constant defined by EVM types once it's ready.
	DefaultLookbackBlocks         = 100
)

func NewCREForwarderCodec() (CREForwarderCodec, error) {
	ABI, err := forwarder.KeystoneForwarderMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return &creForwarderCodecImpl{
		abi: ABI,
	}, nil
}

func NewCREForwarderClient(EVMService types.EVMService, forwarderAddress common.Address, forwarderLookbackBlocks int64, lggr logger.Logger) (CREForwarderClient, error) {
	codec, err := NewCREForwarderCodec()
	if err != nil {
		return nil, err
	}
	if forwarderLookbackBlocks <= 0 {
		lggr.Debugf("forwarderLookbackBlocks is set to a zero/negative value %d, using default value of %d.", forwarderLookbackBlocks, DefaultLookbackBlocks)
		forwarderLookbackBlocks = DefaultLookbackBlocks
	}
	return &creForwarderClient{
		evmService:              EVMService,
		forwarderCodec:          codec,
		forwarderAddress:        forwarderAddress,
		forwarderLookbackBlocks: forwarderLookbackBlocks,
		logger:                  lggr,
	}, nil
}

func (cfclient *creForwarderClient) GetReportProcessedEvents(ctx context.Context, receiver common.Address, workflowExecutionID [32]byte, reportID [2]byte) ([]*evm.Log, error) {
	latest, err := cfclient.evmService.HeaderByNumber(ctx, evm.HeaderByNumberRequest{Number: big.NewInt(rpc.LatestBlockNumber.Int64())})
	if err != nil {
		return nil, err
	}
	if latest.Header == nil {
		return nil, fmt.Errorf("latest block header is nil")
	}
	sub := big.NewInt(cfclient.forwarderLookbackBlocks)
	fromBlock := new(big.Int).Sub(latest.Header.Number, sub)
	if fromBlock.Sign() == -1 {
		fromBlock = big.NewInt(0)
	}

	filterQuery := evmtypes.FilterQuery{
		Addresses: []evmtypes.Address{evmtypes.Address(cfclient.forwarderAddress.Bytes())},
		Topics: [][]evmtypes.Hash{
			{cfclient.forwarderCodec.GetReportProcessedTopicHash()},
			{evmtypes.Hash(common.LeftPadBytes(receiver.Bytes(), common.HashLength))},
			{evmtypes.Hash(common.LeftPadBytes(workflowExecutionID[:], common.HashLength))},
			{padBytes2ToBytes32(reportID)},
		},
		FromBlock: fromBlock,
	}
	reply, err := cfclient.evmService.FilterLogs(ctx, evmtypes.FilterLogsRequest{
		FilterQuery: filterQuery, ConfidenceLevel: primitives.Unconfirmed,
	})
	if err != nil {
		return nil, err
	}

	return reply.Logs, nil
}

// padBytes2ToBytes32 right-pads a bytes2 value to 32 bytes (Solidity-compatible)
func padBytes2ToBytes32(b2 [2]byte) common.Hash {
	var padded [32]byte
	copy(padded[:2], b2[:]) // copy first 2 bytes
	return common.BytesToHash(padded[:])
}

type CREForwarderClient interface {
	GetTransmissionInfo(ctx context.Context, transmissionID TransmissionID) (TransmissionInfo, error)
	InvokeOnReport(ctx context.Context, receiverAddress common.Address, report *workflowpb.ReportResponse, gasConfig *evmcap.GasConfig) (*evmtypes.TransactionResult, error)
	GetReportProcessedEvents(ctx context.Context, receiver common.Address, workflowExecutionID [32]byte, reportID [2]byte) ([]*evm.Log, error)
}

type CREForwarderCodec interface {
	EncodeQueryTransmissionInputs(query QueryTransmissionInputs) ([]byte, error)
	DecodeQueryTransmissionInfo(encodedData []byte) (TransmissionInfo, error)
	EncodeReport(receiver common.Address, report *workflowpb.ReportResponse) ([]byte, error)
	GetReportProcessedTopicHash() evmtypes.Hash
	DecodeForwarderError(err error) (ForwarderErrorType, error)
}

func (cfc *creForwarderCodecImpl) GetReportProcessedTopicHash() evmtypes.Hash {
	return cfc.abi.Events["ReportProcessed"].ID
}

type creForwarderClient struct {
	evmService              types.EVMService
	forwarderCodec          CREForwarderCodec
	forwarderAddress        common.Address
	forwarderLookbackBlocks int64
	logger                  logger.Logger
}

func (cfclient *creForwarderClient) InvokeOnReport(ctx context.Context, receiverAddress common.Address, report *workflowpb.ReportResponse, gasConfig *evmcap.GasConfig) (*evmtypes.TransactionResult, error) {
	if gasConfig == nil || gasConfig.GasLimit == 0 {
		return nil, fmt.Errorf("gas limit shouldn't be unset")
	}

	encodedReport, err := cfclient.forwarderCodec.EncodeReport(receiverAddress, report)
	if err != nil {
		return nil, err
	}

	_, err = cfclient.evmService.CallContract(ctx, evmtypes.CallContractRequest{
		Msg: &evmtypes.CallMsg{
			To:   cfclient.forwarderAddress,
			Data: encodedReport,
		},
	})

	if err != nil {
		// TODO ignore this error if we know that it ended up recorder onchain as a transmisison failure
		errName, err := cfclient.forwarderCodec.DecodeForwarderError(err)
		if err != nil {
			// TODO enrich err
			return nil, err
		}
		return nil, fmt.Errorf("tx reverted before report submission because of: %s", errName)
	}

	// TODO: PLEX-1522 - Add support to limit maximum total fee based on billing config
	transactionResult, err := cfclient.evmService.SubmitTransaction(ctx, evmtypes.SubmitTransactionRequest{
		To:   cfclient.forwarderAddress,
		Data: encodedReport,
		GasConfig: &evmtypes.GasConfig{
			GasLimit: &gasConfig.GasLimit,
		},
	})
	if err != nil {
		if errors.Is(err, types.ErrSettingTransactionGasLimitNotSupported) {
			return cfclient.evmService.SubmitTransaction(ctx, evmtypes.SubmitTransactionRequest{
				To: cfclient.forwarderAddress, Data: encodedReport,
			})
		}
		return nil, fmt.Errorf("failed to submit transaction: %w", err)
	}

	return transactionResult, nil
}

func (cfclient *creForwarderClient) GetTransmissionInfo(ctx context.Context, transmissionID TransmissionID) (TransmissionInfo, error) {
	queryInputs := QueryTransmissionInputs{
		Receiver:            transmissionID.ReceiverHex(),
		WorkflowExecutionID: transmissionID.WorkflowExecutionID,
		ReportID:            transmissionID.ReportID}
	calldata, err := cfclient.forwarderCodec.EncodeQueryTransmissionInputs(queryInputs)
	if err != nil {
		return TransmissionInfo{}, err
	}
	response, err := cfclient.evmService.CallContract(ctx, evmtypes.CallContractRequest{
		Msg: &evmtypes.CallMsg{
			To:   cfclient.forwarderAddress,
			Data: calldata,
		},
		BlockNumber: big.NewInt(LatestBlock),
	})
	if err != nil {
		return TransmissionInfo{}, err
	}
	return cfclient.forwarderCodec.DecodeQueryTransmissionInfo(response.Data)
}

func (cfclient *creForwarderClient) GetCodec() CREForwarderCodec {
	return cfclient.forwarderCodec
}

type creForwarderCodecImpl struct {
	abi *abi.ABI
}

// EncodeReport implements KeystoneForwarderCodec.
func (cfc *creForwarderCodecImpl) EncodeReport(receiver common.Address, report *workflowpb.ReportResponse) ([]byte, error) {
	// Note: The codec that ChainWriter uses to encode the parameters for the contract ABI cannot handle
	// `nil` values, including for slices. Until the bug is fixed we need to ensure that there are no
	// `nil` values passed in the request.
	var signatures [][]byte
	for _, sig := range report.Sigs {
		signatures = append(signatures, sig.Signature)
	}

	req := struct {
		Receiver      common.Address
		RawReport     []byte
		ReportContext []byte
		Signatures    [][]byte
	}{receiver, report.RawReport, report.ReportContext, signatures}

	if req.RawReport == nil {
		req.RawReport = make([]byte, 0)
	}

	if req.ReportContext == nil {
		req.ReportContext = make([]byte, 0)
	}

	if req.Signatures == nil {
		req.Signatures = make([][]byte, 0)
	}
	return cfc.abi.Pack("report", req.Receiver, req.RawReport, req.ReportContext, req.Signatures)
}

type QueryTransmissionInputs struct {
	Receiver            string
	WorkflowExecutionID [32]byte
	ReportID            [2]byte
}

func (cfc *creForwarderCodecImpl) EncodeQueryTransmissionInputs(query QueryTransmissionInputs) ([]byte, error) {
	return cfc.abi.Pack("getTransmissionInfo", common.HexToAddress(query.Receiver), query.WorkflowExecutionID, query.ReportID)
}

func (cfc *creForwarderCodecImpl) DecodeQueryTransmissionInfo(encodedData []byte) (TransmissionInfo, error) {
	var transmissionInfo TransmissionInfo
	values, err := cfc.abi.Methods["getTransmissionInfo"].Outputs.UnpackValues(encodedData)
	if err != nil {
		return TransmissionInfo{}, errors.Join(errors.New("failed to abi unpack getTransmissionInfo return data"), err)
	}
	value := values[0]
	byt, err := json.Marshal(value)
	if err != nil {
		return TransmissionInfo{}, errors.Join(errors.New("failed to marshal getTransmissionInfo return data"), err)
	}

	if err = json.Unmarshal(byt, &transmissionInfo); err != nil {
		return TransmissionInfo{}, errors.Join(errors.New("failed to unmarshal getTransmissionInfo return data"), err)
	}

	return transmissionInfo, err
}

// TransmissionID holds the unique ID for searching a TX related to the report transmission to keystone forwarder
type TransmissionID struct {
	Receiver            common.Address
	ReportID            [2]byte
	WorkflowExecutionID [32]byte
}

func (t TransmissionID) ReceiverHex() string {
	return common.Bytes2Hex(t.Receiver[:])
}

func (t TransmissionID) GetIDPartsForDebugging() []any {
	return []any{"receiver", common.Bytes2Hex(t.Receiver[:]), "reportID", common.Bytes2Hex(t.ReportID[:]), "workflowExecutionID", common.Bytes2Hex(t.WorkflowExecutionID[:])}
}

func (t TransmissionID) GetDebugID() string {
	return fmt.Sprintf("receiver: %s, reportID: %s, workflowExecutionID %s", t.ReceiverHex(), common.Bytes2Hex(t.ReportID[:]), common.Bytes2Hex(t.WorkflowExecutionID[:]))
}

func (cfc *creForwarderCodecImpl) DecodeForwarderError(err error) (ForwarderErrorType, error) {
	if err == nil {
		return "", nil
	}

	var dataErr rpc.DataError
	if !errors.As(err, &dataErr) {
		return ForwarderErrorUnknown, fmt.Errorf("call contract failed without rpc revert data: %w", err)
	}

	revertHex, ok := extractHexRevertData(dataErr.ErrorData())
	if !ok {
		return ForwarderErrorUnknown, fmt.Errorf("call contract failed but no hex revert data found: %w", err)
	}

	revertData := common.FromHex(revertHex)
	if len(revertData) < 4 {
		return ForwarderErrorUnknown, fmt.Errorf("revert data shorter than 4-byte selector: %s", revertHex)
	}

	selector := revertData[:4]

	if bytes.Equal(selector, []byte{0x08, 0xc3, 0x79, 0xa0}) {
		return ForwarderErrorStandardErrorString, nil
	}

	if bytes.Equal(selector, []byte{0x4e, 0x48, 0x7b, 0x71}) {
		return ForwarderErrorStandardPanic, nil
	}

	for name, abiErr := range cfc.abi.Errors {
		id := abiErr.ID.Bytes()
		if len(id) >= 4 && bytes.Equal(selector, id[:4]) {
			if typ, ok := forwarderErrorNameToType[name]; ok {
				return typ, nil
			}
			return ForwarderErrorUnknown, nil
		}
	}

	return ForwarderErrorUnknown, nil
}

func extractHexRevertData(v interface{}) (string, bool) {
	switch t := v.(type) {
	case string:
		if _, err := common.ParseHexOrString(t); err == nil {
			return t, true
		}
	case map[string]interface{}:
		for _, k := range []string{"data", "result", "output"} {
			if vv, ok := t[k]; ok {
				if s, ok := extractHexRevertData(vv); ok {
					return s, true
				}
			}
		}
		for _, vv := range t {
			if s, ok := extractHexRevertData(vv); ok {
				return s, true
			}
		}
	case []interface{}:
		for _, vv := range t {
			if s, ok := extractHexRevertData(vv); ok {
				return s, true
			}
		}
	}
	return "", false
}
