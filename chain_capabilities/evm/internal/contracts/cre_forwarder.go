package contracts

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	evmcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/forwarder"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

type TransmissionInfo struct {
	GasLimit        *big.Int `json:"gasLimit,omitempty"`
	InvalidReceiver bool     `json:"invalidReceiver,omitempty"`
	State           uint8    `json:"state,omitempty"`
	Success         bool     `json:"success,omitempty"`
	//nolint:revive
	TransmissionId [32]byte       `json:"transmissionId,omitempty"`
	Transmitter    common.Address `json:"transmitter,omitempty"`
}

// The gas cost of the forwarder contract logic, including state updates and event emission.
// This is a rough estimate and should be updated if the forwarder contract logic changes.
// PLEX-1524 - Make the forwarder contract logic gas cost limit configurable
const (
	ForwarderContractLogicGasCost = 100_000
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

func NewCREForwarderClient(EVMService types.EVMService, forwarderAddress common.Address, forwarderLookbackBlocks int64, logger logger.Logger) (CREForwarderClient, error) {
	codec, err := NewCREForwarderCodec()
	if err != nil {
		return nil, err
	}
	if forwarderLookbackBlocks <= 0 {
		logger.Debugf("forwarderLookbackBlocks is set to a zero/negative value %d, using default value of %d.", forwarderLookbackBlocks, DefaultLookbackBlocks)
		forwarderLookbackBlocks = DefaultLookbackBlocks
	}
	return &creForwarderClient{
		evmService:              EVMService,
		forwarderCodec:          codec,
		forwarderAddress:        forwarderAddress,
		forwarderLookbackBlocks: forwarderLookbackBlocks,
		logger:                  logger,
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
	cfclient.logger.Debugw("Transaction raw report", "report", hex.EncodeToString(report.RawReport))

	var resolvedGasConfig *evmtypes.GasConfig
	if gasConfig != nil && gasConfig.GasLimit > 0 {
		resolvedGasConfig = &evmtypes.GasConfig{
			GasLimit: &gasConfig.GasLimit,
		}
	}
	encodedReport, err := cfclient.forwarderCodec.EncodeReport(receiverAddress, report)
	if err != nil {
		return nil, err
	}
	// TODO: PLEX-1522 - Add support to limit maximum total fee based on billing config
	transactionResult, err := cfclient.evmService.SubmitTransaction(ctx, evmtypes.SubmitTransactionRequest{
		To:        cfclient.forwarderAddress,
		Data:      encodedReport,
		GasConfig: resolvedGasConfig,
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
	// PLEX-1524 this is ugly. For some reason ABI.UnpackIntoInterface doesn't work.
	var transmissionInfo TransmissionInfo
	values, err := cfc.abi.Methods["getTransmissionInfo"].Outputs.UnpackValues(encodedData)
	if err != nil {
		return TransmissionInfo{}, errors.Join(errors.New("failed to abi unpack getTransmissionInfo return data"), err)
	}
	value := values[0]
	bytes, err := json.Marshal(value)
	if err != nil {
		return TransmissionInfo{}, errors.Join(errors.New("failed to marshal getTransmissionInfo return data"), err)
	}

	if err = json.Unmarshal(bytes, &transmissionInfo); err != nil {
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
