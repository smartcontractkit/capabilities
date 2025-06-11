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
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	evmcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"

	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/forwarder"
)

//Transmission Info
// bytes32 transmissionId;
// TransmissionState state;
// address transmitter;
// // This is true if the receiver is not a contract or does not implement the
// // `IReceiver` interface.
// bool invalidReceiver;
// // Whether the transmission attempt was successful. If `false`, the
// // transmission can be retried with an increased gas limit.
// bool success;
// // The amount of gas allocated for the `IReceiver.onReport` call. uint80
// // allows storing gas for known EVM block gas limits.
// // Ensures that the minimum gas requested by the user is available during
// // the transmission attempt. If the transmission fails (indicated by a
// // `false` success state), it can be retried with an increased gas limit.
// uint80 gasLimit;

type TransmissionInfo struct {
	GasLimit        *big.Int
	InvalidReceiver bool
	State           uint8
	Success         bool
	TransmissionId  [32]byte
	Transmitter     common.Address
}

// The gas cost of the forwarder contract logic, including state updates and event emission.
// This is a rough estimate and should be updated if the forwarder contract logic changes.
// TODO: Make this part of the on-chain capability configuration
const ForwarderContractLogicGasCost = 100_000

func NewKeystoneForwarderCodec() (KeystoneForwarderCodec, error) {
	ABI, err := forwarder.KeystoneForwarderMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return &keystoneForwarderCodecImpl{
		abi: ABI,
	}, nil
}

func NewKeystoneForwarderClient(EVMService types.EVMService, forwarderAddress common.Address, logger logger.Logger) (KeystoneForwarderClient, error) {
	codec, err := NewKeystoneForwarderCodec()
	if err != nil {
		return nil, err
	}
	return &keystoneForwarderClient{
		EVMService:       EVMService,
		ForwarderCodec:   codec,
		ForwarderAddress: forwarderAddress,
		Logger:           logger,
	}, nil

}

type KeystoneForwarderClient interface {
	GetTransmissionInfo(ctx context.Context, transmissionID TransmissionID) (TransmissionInfo, error)
	InvokeOnReport(ctx context.Context, receiverAddress common.Address, report *evmcap.SignedReport, gasConfig *evmcap.GasConfig) (*evmtypes.TransactionResult, error)
}

type KeystoneForwarderCodec interface {
	EncodeQueryTransmissionInputs(query QueryTransmissionInputs) ([]byte, error)
	DecodeQueryTransmissionInfo(encodedData []byte) (TransmissionInfo, error)
	EncodeReport(receiver common.Address, report *evmcap.SignedReport) ([]byte, error)
}

type keystoneForwarderClient struct {
	EVMService       types.EVMService
	ForwarderCodec   KeystoneForwarderCodec
	ForwarderAddress common.Address
	Logger           logger.Logger
}

func (kfclient *keystoneForwarderClient) InvokeOnReport(ctx context.Context, receiverAddress common.Address, report *evmcap.SignedReport, gasConfig *evmcap.GasConfig) (*evmtypes.TransactionResult, error) {

	kfclient.Logger.Debugw("Transaction raw report", "report", hex.EncodeToString(report.RawReport))

	// meta := commontypes.TxMeta{WorkflowExecutionID: &request.Metadata.WorkflowExecutionID}
	var resolvedGasConfig *evmtypes.GasConfig
	if gasConfig != nil && gasConfig.GasLimit > 0 {
		resolvedGasConfig = &evmtypes.GasConfig{
			GasLimit: &gasConfig.GasLimit,
		}
		// meta.GasLimit = new(big.Int).SetUint64(request.GasConfig.GasLimit)
	}
	encodedReport, err := kfclient.ForwarderCodec.EncodeReport(receiverAddress, report)
	if err != nil {
		return nil, err
	}
	// TODO: PLEX-1522 - Add support to limit maximum total fee based on billing config
	transactionResult, err := kfclient.EVMService.SubmitTransaction(ctx, evmtypes.SubmitTransactionRequest{
		To:        kfclient.ForwarderAddress,
		Data:      encodedReport,
		GasConfig: resolvedGasConfig,
	})

	if err != nil {
		if types.ErrSettingTransactionGasLimitNotSupported.Is(err) {
			return kfclient.EVMService.SubmitTransaction(ctx, evmtypes.SubmitTransactionRequest{
				To:   kfclient.ForwarderAddress,
				Data: encodedReport,
			})
		}
		return nil, fmt.Errorf("failed to submit transaction: %w", err)
	}
	return transactionResult, nil
}

func (kfclient *keystoneForwarderClient) GetTransmissionInfo(ctx context.Context, transmissionID TransmissionID) (TransmissionInfo, error) {
	queryInputs := QueryTransmissionInputs{
		Receiver:            transmissionID.ReceiverHex(),
		WorkflowExecutionID: transmissionID.WorkflowExecutionID,
		ReportID:            transmissionID.ReportID,
	}
	calldata, err := kfclient.ForwarderCodec.EncodeQueryTransmissionInputs(queryInputs)
	if err != nil {
		return TransmissionInfo{}, err
	}
	response, err := kfclient.EVMService.CallContract(ctx, &evmtypes.CallMsg{
		To:   kfclient.ForwarderAddress,
		Data: calldata,
	}, big.NewInt(-1))
	if err != nil {
		return TransmissionInfo{}, err
	}
	return kfclient.ForwarderCodec.DecodeQueryTransmissionInfo(response)
}

type keystoneForwarderCodecImpl struct {
	abi *abi.ABI
}

// EncodeReport implements KeystoneForwarderCodec.
func (kfc *keystoneForwarderCodecImpl) EncodeReport(receiver common.Address, report *evmcap.SignedReport) ([]byte, error) {
	// Note: The codec that ChainWriter uses to encode the parameters for the contract ABI cannot handle
	// `nil` values, including for slices. Until the bug is fixed we need to ensure that there are no
	// `nil` values passed in the request.
	req := struct {
		Receiver      common.Address
		RawReport     []byte
		ReportContext []byte
		Signatures    [][]byte
	}{receiver, report.RawReport, report.ReportContext, report.Signatures}

	if req.RawReport == nil {
		req.RawReport = make([]byte, 0)
	}

	if req.ReportContext == nil {
		req.ReportContext = make([]byte, 0)
	}

	if req.Signatures == nil {
		req.Signatures = make([][]byte, 0)
	}
	return kfc.abi.Pack("report", req.Receiver, req.RawReport, req.ReportContext, req.Signatures)
}

type QueryTransmissionInputs struct {
	Receiver            string
	WorkflowExecutionID [32]byte
	ReportID            [2]byte
}

func (kfc *keystoneForwarderCodecImpl) EncodeQueryTransmissionInputs(query QueryTransmissionInputs) ([]byte, error) {
	return kfc.abi.Pack("getTransmissionInfo", common.HexToAddress(query.Receiver), query.WorkflowExecutionID, query.ReportID)
}

func (kfc *keystoneForwarderCodecImpl) DecodeQueryTransmissionInfo(encodedData []byte) (TransmissionInfo, error) {
	//TODO this is ugly. For some reason ABI.UnpackIntoInterface doesn't work.
	var transmissionInfo TransmissionInfo
	values, err := kfc.abi.Methods["getTransmissionInfo"].Outputs.UnpackValues(encodedData)
	if err != nil {
		return TransmissionInfo{}, errors.Join(errors.New("Failed to decode getTransmissionInfo return data"), err)
	}
	value := values[0]
	bytes, err := json.Marshal(value)
	json.Unmarshal(bytes, &transmissionInfo)
	return transmissionInfo, err
}

// This ID holds the unique ID for searching a TX related to the report transmission to keystone forwarder
type TransmissionID struct {
	Receiver            common.Address
	ReportID            [2]byte
	WorkflowExecutionID [32]byte
}

func (t TransmissionID) ReceiverHex() string {
	return common.Bytes2Hex(t.Receiver[:])
}

func (t TransmissionID) GetIDPartsForDebugging() []interface{} {
	return []interface{}{"receiver", common.Bytes2Hex(t.Receiver[:]), "reportID", t.ReportID, "workflowExecutionID", t.WorkflowExecutionID}
}

func (t TransmissionID) GetDebugID() string {
	return fmt.Sprintf("receiver: %s, reportID: %s, workflowExecutionID %s", t.ReceiverHex(), common.Bytes2Hex(t.ReportID[:]), common.Bytes2Hex(t.WorkflowExecutionID[:]))
}
