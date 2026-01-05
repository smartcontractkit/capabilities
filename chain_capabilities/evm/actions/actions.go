package actions

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-framework/multinode"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	ctypes "github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"
)

type ConsensusHandler interface {
	// Handle - returns a channel to the result of `request.GetObservation()`. This result is consistent across all nodes in
	// the DON, even if individual RPC states differ.
	Handle(ctx context.Context, request ctypes.Request) (<-chan ctypes.Reply, error)
}

type EVM struct {
	types.EVMService
	ConsensusHandler         ConsensusHandler
	chainSelector            uint64
	keystoneForwarderAddress common.Address
	forwarderClient          contracts.CREForwarderClient
	ReceiverGasMinimum       uint64
	LookbackBlocks           uint64

	lggr              logger.SugaredLogger
	beholderProcessor beholder.ProtoProcessor
	messageBuilder    *monitoring.MessageBuilder

	readPayloadSizeLimiter limits.BoundLimiter[commoncfg.Size]
	logQueryBlockLimit     limits.BoundLimiter[uint64]
	reportSizeLimit        limits.BoundLimiter[commoncfg.Size]
	txGasLimit             limits.BoundLimiter[uint64]
}

func NewEVM(cfg config.Config, evmService types.EVMService, lggr logger.Logger, beholderProcessor beholder.ProtoProcessor,
	messageBuilder *monitoring.MessageBuilder, handler ConsensusHandler, chainSelector uint64, limitsFactory limits.Factory) (*EVM, caperrors.Error) {
	keystoneForwarderAddress := common.HexToAddress(cfg.CREForwarderAddress)
	if keystoneForwarderAddress == (common.Address{}) {
		return &EVM{}, caperrors.NewPublicSystemError(errors.New("keystone forwarder address is not set"), caperrors.Unknown)
	}

	kfc, err := contracts.NewCREForwarderClient(evmService, keystoneForwarderAddress, cfg.ForwarderLookbackBlocks, lggr)
	if err != nil {
		return &EVM{}, caperrors.NewPublicSystemError(err, caperrors.Unknown)
	}

	e := &EVM{
		EVMService:               evmService,
		keystoneForwarderAddress: keystoneForwarderAddress,
		forwarderClient:          kfc,
		ReceiverGasMinimum:       cfg.ReceiverGasMinimum,
		lggr:                     logger.Sugared(lggr),
		beholderProcessor:        beholderProcessor,
		messageBuilder:           messageBuilder,
		ConsensusHandler:         handler,
		chainSelector:            chainSelector,
	}
	err = e.initLimiters(limitsFactory)
	if err != nil {
		return e, caperrors.NewPublicSystemError(err, caperrors.Internal)
	}

	return e, nil
}

func (e *EVM) initLimiters(limitsFactory limits.Factory) (err error) {
	e.readPayloadSizeLimiter, err = limits.MakeBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainRead.PayloadSizeLimit)
	if err != nil {
		return
	}
	e.logQueryBlockLimit, err = limits.MakeBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainRead.LogQueryBlockLimit)
	if err != nil {
		return
	}
	e.reportSizeLimit, err = limits.MakeBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainWrite.ReportSizeLimit)
	if err != nil {
		return
	}
	e.txGasLimit, err = limits.MakeBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainWrite.EVM.GasLimit)
	return
}

func (e *EVM) Close() error {
	return services.CloseAll(e.readPayloadSizeLimiter, e.logQueryBlockLimit, e.reportSizeLimit, e.txGasLimit)
}

func requestID(meta capabilities.RequestMetadata) string {
	return commonMon.RequestID(meta.WorkflowExecutionID, meta.ReferenceID)
}

func (e *EVM) CallContract(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	input *evm.CallContractRequest,
) (*capabilities.ResponseAndMetadata[*evm.CallContractReply], caperrors.Error) {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}

	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.CallContract)); err != nil {
		return nil, NewUserError(err)
	}

	callMsg, err := evm.ConvertCallMsgFromProto(input.GetCall())
	if err != nil {
		return nil, NewUserError(err)
	}

	blockNumber, needsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(input.GetBlockNumber())
	if err != nil {
		return nil, NewUserError(err)
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractInitiated(telemetryContext, callMsg, blockNumber.Int64()))

	callContract := func(ctx context.Context, blockNumber *big.Int) ([]byte, error) {
		// TODO: PLEX-1558 agree on RPC error content
		resp, err := e.EVMService.CallContract(ctx, evmtypes.CallContractRequest{
			Msg:             callMsg,
			BlockNumber:     blockNumber,
			ConfidenceLevel: confidenceLevel,
			IsExternal:      true,
		})
		if err != nil {
			return nil, err
		}
		return resp.Data, nil
	}

	var request ctypes.Request
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID(meta), func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
			callBlockNumber, err := getCallBlockNumber(blockNumber, height)
			if err != nil {
				return nil, caperrors.NewPublicSystemError(fmt.Errorf("error getting call block number: %w", err), caperrors.Unknown)
			}

			return callContract(ctx, callBlockNumber)
		})
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return callContract(ctx, big.NewInt(blockNumber.Int64()))
		})
	}

	data, err := readType[[]byte](ctx, e.ConsensusHandler, request)
	if err != nil {
		isUserError := e.isUserError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildCallContractError(telemetryContext, callMsg, blockNumber.Int64(), "Failed to read CallContract", err.Error(), isUserError))
		return nil, GetError(err, isUserError)
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read CallContract", e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractSuccess(telemetryContext, callMsg, blockNumber.Int64()))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.CallContractReply]{
		Response:         &evm.CallContractReply{Data: data},
		ResponseMetadata: metering.GetResponseMetadata(metering.CallContract),
	}
	return &responseAndMetadata, nil
}

func (e *EVM) filterLogsToRequest(ctx context.Context, meta capabilities.RequestMetadata, ethFilterQuery evmtypes.FilterQuery) (ctypes.Request, error) {
	filterLogs := func(ctx context.Context, query evmtypes.FilterQuery, confidenceLevel primitives.ConfidenceLevel) ([]byte, error) {
		reply, err := e.EVMService.FilterLogs(ctx, evmtypes.FilterLogsRequest{
			FilterQuery:     query,
			ConfidenceLevel: confidenceLevel,
			IsExternal:      true,
		})
		if err != nil {
			return nil, GetError(err, e.isUserError(err))
		}

		logs, err := evm.ConvertLogsToProto(reply.Logs)
		if err != nil {
			return nil, fmt.Errorf("failed to convert logs to proto: %w", err)
		}

		b, err := proto.Marshal(&evm.FilterLogsReply{Logs: logs})
		if err != nil {
			return nil, err
		}
		if err = e.readPayloadSizeLimiter.Check(ctx, commoncfg.SizeOf(b)); err != nil {
			return nil, NewUserError(err)
		}
		return b, nil
	}

	if ethFilterQuery.BlockHash != (evmtypes.Hash{}) {
		if ethFilterQuery.FromBlock != nil || ethFilterQuery.ToBlock != nil {
			return nil, NewUserError(errors.New("cannot specify both block hash and block range"))
		}

		return ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return filterLogs(ctx, ethFilterQuery, primitives.Unconfirmed)
		}), nil
	}

	fromBlock, fromNeedsBlockHeightConsensus, _, err := normalizeBlockNumber(valuespb.NewBigIntFromInt(ethFilterQuery.FromBlock))
	if err != nil {
		return nil, NewUserError(fmt.Errorf("fromBlock is invalid: %w", err))
	}

	toBlock, toNeedsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(valuespb.NewBigIntFromInt(ethFilterQuery.ToBlock))
	if err != nil {
		return nil, NewUserError(fmt.Errorf("toBlock is invalid: %w", err))
	}

	if !fromNeedsBlockHeightConsensus && !toNeedsBlockHeightConsensus {
		err = e.validateBlockRange(ctx, ethFilterQuery.FromBlock, ethFilterQuery.ToBlock)
		if err != nil {
			return nil, NewUserError(err)
		}
		return ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return filterLogs(ctx, ethFilterQuery, confidenceLevel)
		}), nil
	}

	return ctypes.NewLockableToBlockRequest(requestID(meta), func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
		callFromBlock, err := getCallBlockNumber(fromBlock, height)
		if err != nil {
			return nil, fmt.Errorf("error getting callFromBlock: %w", err)
		}

		callToBlock, err := getCallBlockNumber(toBlock, height)
		if err != nil {
			return nil, fmt.Errorf("error getting callToBlock: %w", err)
		}

		err = e.validateBlockRange(ctx, callFromBlock, callToBlock)
		if err != nil {
			return nil, NewUserError(err)
		}

		ethFilterQuery.FromBlock = big.NewInt(callFromBlock.Int64())
		ethFilterQuery.ToBlock = big.NewInt(callToBlock.Int64())

		return filterLogs(ctx, ethFilterQuery, confidenceLevel)
	}), nil
}

func (e *EVM) validateBlockRange(ctx context.Context, fromBlock, toBlock *big.Int) error {
	rangeSize := big.NewInt(0).Sub(toBlock, fromBlock)
	if rangeSize.Sign() < 0 {
		return fmt.Errorf("toBlock %s is less than fromBlock %s", toBlock.String(), fromBlock.String())
	}

	if !rangeSize.IsUint64() {
		return fmt.Errorf("block range size %s overflows uint64", rangeSize)
	}

	return e.logQueryBlockLimit.Check(ctx, rangeSize.Uint64())
}

func (e *EVM) FilterLogs(ctx context.Context, meta capabilities.RequestMetadata, req *evm.FilterLogsRequest) (*capabilities.ResponseAndMetadata[*evm.FilterLogsReply], caperrors.Error) {
	ctx = meta.ContextWithCRE(ctx)
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}

	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.FilterLogs)); err != nil {
		return nil, NewUserError(err)
	}

	ethFilterQuery, err := evm.ConvertFilterFromProto(req.GetFilterQuery())
	if err != nil {
		return nil, NewUserError(err)
	}

	request, err := e.filterLogsToRequest(ctx, meta, ethFilterQuery)
	if err != nil {
		return nil, EnsureRemoteReportable(err)
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsInitiated(telemetryContext, ethFilterQuery))

	var reply evm.FilterLogsReply
	err = e.readProto(ctx, request, &reply)
	if err != nil {
		isUserError := e.isUserError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildFilterLogsError(telemetryContext, ethFilterQuery, "Failed to FilterLogs", err.Error(), isUserError))
		return nil, GetError(err, isUserError)
	}

	// G115: integer overflow conversion int -> int32 (gosec)
	// nolint:gosec
	monitoring.LogAndEmitSuccess(ctx, "Successfully executed FilterLogs", e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsSuccess(telemetryContext, ethFilterQuery, int32(len(reply.Logs))))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.FilterLogsReply]{
		Response:         &reply,
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	return &responseAndMetadata, nil
}

func (e *EVM) BalanceAt(ctx context.Context, meta capabilities.RequestMetadata, req *evm.BalanceAtRequest) (*capabilities.ResponseAndMetadata[*evm.BalanceAtReply], caperrors.Error) {
	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.BalanceAt)); err != nil {
		return nil, NewUserError(err)
	}
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	blockNumber, needsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(req.GetBlockNumber())
	if err != nil {
		return nil, NewUserError(err)
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildBalanceAtInitiated(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64()))

	balanceAt := func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
		callBlockNumber, err := getCallBlockNumber(blockNumber, height)
		if err != nil {
			return nil, NewUserError(fmt.Errorf("error getting call block number: %w", err))
		}

		address, err := evmservice.ConvertOptionalAddressFromProto(req.GetAccount())
		if err != nil {
			return nil, NewUserError(fmt.Errorf("error converting address from proto: %w", err))
		}

		reply, err := e.EVMService.BalanceAt(ctx, evmtypes.BalanceAtRequest{
			Address:         address,
			BlockNumber:     callBlockNumber,
			ConfidenceLevel: confidenceLevel,
		})
		if err != nil {
			return nil, err
		}

		pbBalance := valuespb.NewBigIntFromInt(reply.Balance)
		return proto.Marshal(pbBalance)
	}

	var request ctypes.Request
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID(meta), balanceAt)
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return balanceAt(ctx, nil)
		})
	}

	balance := new(valuespb.BigInt)
	if err := e.readProto(ctx, request, balance); err != nil {
		isUserError := e.isUserError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildBalanceAtError(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64(), "Failed to read BalanceAt", err.Error(), isUserError))
		return nil, GetError(err, isUserError)
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read BalanceAt", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildBalanceAtSuccess(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64(), valuespb.NewIntFromBigInt(balance)))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.BalanceAtReply]{
		Response:         &evm.BalanceAtReply{Balance: balance},
		ResponseMetadata: metering.GetResponseMetadata(metering.BalanceAt),
	}
	return &responseAndMetadata, nil
}

func (e *EVM) EstimateGas(ctx context.Context, meta capabilities.RequestMetadata, req *evm.EstimateGasRequest) (*capabilities.ResponseAndMetadata[*evm.EstimateGasReply], caperrors.Error) {
	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.EstimateGas)); err != nil {
		return nil, NewUserError(err)
	}
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	msg, err := evm.ConvertCallMsgFromProto(req.GetMsg())
	if err != nil {
		return nil, NewUserError(err)
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildEstimateGasInitiated(telemetryContext, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data))

	request := ctypes.NewAggregatableRequest(requestID(meta), func(ctx context.Context) (*ctypes.AggregatableObservation, error) {
		rawEstimate, err := e.EVMService.EstimateGas(ctx, msg)
		if err != nil {
			return nil, err
		}

		estimate := &valuespb.Decimal{
			Coefficient: valuespb.NewBigIntFromInt(big.NewInt(0).SetUint64(rawEstimate)),
			Exponent:    0,
		}

		return &ctypes.AggregatableObservation{
			Method: ctypes.AggregationMethodFPlusOneHighest,
			Value:  estimate,
		}, nil
	})

	rawEstimate, err := readDecimal(ctx, e.ConsensusHandler, request)
	if err != nil {
		isUserError := e.isUserError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildEstimateGasError(telemetryContext, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, "Failed to execute EstimateGas", err.Error(), isUserError))
		return nil, GetError(err, isUserError)
	}

	logMsg := e.messageBuilder.BuildEstimateGasSuccess(telemetryContext, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, rawEstimate.BigInt().Int64())
	monitoring.LogAndEmitSuccess(ctx, "Successfully read EstimateGas", e.lggr, e.beholderProcessor, logMsg)
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.EstimateGasReply]{
		Response:         &evm.EstimateGasReply{Gas: rawEstimate.BigInt().Uint64()},
		ResponseMetadata: metering.GetResponseMetadata(metering.EstimateGas),
	}
	return &responseAndMetadata, nil
}

func (e *EVM) GetTransactionByHash(ctx context.Context, meta capabilities.RequestMetadata, req *evm.GetTransactionByHashRequest) (*capabilities.ResponseAndMetadata[*evm.GetTransactionByHashReply], caperrors.Error) {
	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.GetTransactionByHash)); err != nil {
		return nil, NewUserError(err)
	}
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	hash, err := evmservice.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, NewUserError(err)
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionByHashInitiated(telemetryContext, common.Bytes2Hex(hash[:])))
	request := ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
		tx, err := e.EVMService.GetTransactionByHash(ctx, evmtypes.GetTransactionByHashRequest{
			Hash:       hash,
			IsExternal: true,
		})
		if err != nil {
			return nil, err
		}

		protoTx, err := evm.ConvertTransactionToProto(tx)
		if err != nil {
			return nil, NewUserError(err)
		}

		return proto.MarshalOptions{Deterministic: true}.Marshal(protoTx)
	})

	var tx evm.Transaction
	if err := e.readProto(ctx, request, &tx); err != nil {
		isUserError := e.isUserError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildGetTransactionByHashError(telemetryContext, common.Bytes2Hex(hash[:]), "Failed to execute GetTransactionByHash", err.Error(), isUserError))
		return nil, GetError(err, isUserError)
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read GetTransactionByHash", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildGetTransactionByHashSuccess(telemetryContext, common.Bytes2Hex(hash[:]), &tx))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.GetTransactionByHashReply]{
		Response:         &evm.GetTransactionByHashReply{Transaction: &tx},
		ResponseMetadata: metering.GetResponseMetadata(metering.GetTransactionByHash),
	}
	return &responseAndMetadata, nil
}

func (e *EVM) GetTransactionReceipt(ctx context.Context, meta capabilities.RequestMetadata, req *evm.GetTransactionReceiptRequest) (*capabilities.ResponseAndMetadata[*evm.GetTransactionReceiptReply], caperrors.Error) {
	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.GetTransactionReceipt)); err != nil {
		return nil, NewUserError(err)
	}
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	hash, err := evmservice.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, NewUserError(err)
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionReceiptInitiated(telemetryContext, common.Bytes2Hex(hash[:])))
	request := ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
		receipt, err := e.EVMService.GetTransactionReceipt(ctx, evmtypes.GeTransactionReceiptRequest{
			Hash:       hash,
			IsExternal: true,
		})
		if err != nil {
			return nil, err
		}

		protoReceipt, err := evm.ConvertReceiptToProto(receipt)
		if err != nil {
			return nil, NewUserError(err)
		}

		return proto.MarshalOptions{Deterministic: true}.Marshal(protoReceipt)
	})

	var receipt evm.Receipt
	if err := e.readProto(ctx, request, &receipt); err != nil {
		isUserError := e.isUserError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildGetTransactionReceiptError(telemetryContext, common.Bytes2Hex(hash[:]), "Failed to get latest and finalized head", err.Error(), isUserError))
		return nil, GetError(err, isUserError)
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read GetTransactionReceiptSuccess", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildGetTransactionReceiptSuccess(telemetryContext, common.Bytes2Hex(hash[:]), &receipt))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.GetTransactionReceiptReply]{
		Response:         &evm.GetTransactionReceiptReply{Receipt: &receipt},
		ResponseMetadata: metering.GetResponseMetadata(metering.GetTransactionReceipt),
	}
	return &responseAndMetadata, nil
}

func (e *EVM) HeaderByNumber(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	req *evm.HeaderByNumberRequest,
) (*capabilities.ResponseAndMetadata[*evm.HeaderByNumberReply], caperrors.Error) {
	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.HeaderByNumber)); err != nil {
		return nil, NewUserError(err)
	}
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	blockNumber, needsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(req.GetBlockNumber())
	if err != nil {
		return nil, NewUserError(err)
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildHeaderByNumberInitiated(telemetryContext, blockNumber.Int64()))

	headerByNumber := func(ctx context.Context, blockNumber *big.Int) ([]byte, error) {
		reply, err := e.EVMService.HeaderByNumber(ctx, evmtypes.HeaderByNumberRequest{
			Number:          blockNumber,
			ConfidenceLevel: confidenceLevel,
		})
		if err != nil {
			return nil, err
		}

		if reply.Header == nil {
			return nil, NewUserError(fmt.Errorf("header is nil"))
		}

		header, err := evm.ConvertHeaderToProto(reply.Header)
		if err != nil {
			return nil, NewUserError(err)
		}

		return proto.Marshal(&evm.HeaderByNumberReply{Header: header})
	}

	var request ctypes.Request
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID(meta), func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
			callBlockNumber, err := getCallBlockNumber(blockNumber, height)
			if err != nil {
				return nil, fmt.Errorf("error getting call block number: %w", err)
			}

			return headerByNumber(ctx, callBlockNumber)
		})
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return headerByNumber(ctx, big.NewInt(blockNumber.Int64()))
		})
	}

	var reply evm.HeaderByNumberReply
	err = e.readProto(ctx, request, &reply)
	if err != nil {
		isUserError := e.isUserError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildHeaderByNumberError(telemetryContext, blockNumber.Int64(), "Failed to get header by number", err.Error(), isUserError))
		return nil, GetError(err, isUserError)
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully got header by number", e.lggr, e.beholderProcessor, e.messageBuilder.BuildHeaderByNumberSuccess(telemetryContext, blockNumber.Int64(), reply.Header))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.HeaderByNumberReply]{
		Response:         &reply,
		ResponseMetadata: metering.GetResponseMetadata(metering.HeaderByNumber),
	}
	return &responseAndMetadata, nil
}

// normalizeBlockNumber - returns:
// number - normalized block number converted to a corresponding tag, if possible
// needsBlockHeightConsensus - true, if DON Nodes need to agree on common height for corresponding tag, before agreeing on request reply.
func normalizeBlockNumber(pbBlockNumber *valuespb.BigInt) (number rpc.BlockNumber, needsBlockHeightConsensus bool, confidenceLevel primitives.ConfidenceLevel, err error) {
	// Replicate EthClient API, that treats nil block number as latest
	if pbBlockNumber == nil {
		return rpc.LatestBlockNumber, true, primitives.Unconfirmed, nil
	}

	bigBlockNumber := valuespb.NewIntFromBigInt(pbBlockNumber)
	if !bigBlockNumber.IsInt64() {
		return 0, false, primitives.Unconfirmed, fmt.Errorf("block number %s is not an int64", bigBlockNumber)
	}

	blockNumber := rpc.BlockNumber(bigBlockNumber.Int64())
	if blockNumber > 0 {
		return blockNumber, false, primitives.Unconfirmed, nil
	}

	switch blockNumber {
	case rpc.SafeBlockNumber:
		confidenceLevel = primitives.Safe
	case rpc.FinalizedBlockNumber:
		confidenceLevel = primitives.Finalized
	case rpc.LatestBlockNumber:
		confidenceLevel = primitives.Unconfirmed
	default:
		return 0, false, primitives.Unconfirmed, fmt.Errorf("block number %d is not supported", blockNumber)
	}

	return blockNumber, true, confidenceLevel, nil
}

func getCallBlockNumber(requestedBlockNumber rpc.BlockNumber, chainHeight *ctypes.ChainHeight) (*big.Int, error) {
	switch requestedBlockNumber {
	case rpc.LatestBlockNumber, rpc.SafeBlockNumber, rpc.FinalizedBlockNumber:
	default:
		return big.NewInt(int64(requestedBlockNumber)), nil
	}

	if chainHeight == nil {
		return nil, fmt.Errorf("chain height is nil")
	}

	switch requestedBlockNumber {
	case rpc.LatestBlockNumber:
		return big.NewInt(chainHeight.Latest), nil
	case rpc.SafeBlockNumber:
		return big.NewInt(chainHeight.Safe), nil
	case rpc.FinalizedBlockNumber:
		return big.NewInt(chainHeight.Finalized), nil
	default:
		return nil, fmt.Errorf("unexpected block number %d", requestedBlockNumber)
	}
}

func readDecimal(ctx context.Context, handler ConsensusHandler, request ctypes.Request) (decimal.Decimal, error) {
	rawDecimal, err := readType[*valuespb.Decimal](ctx, handler, request)
	if err != nil {
		return decimal.Decimal{}, err
	}

	return decimal.NewFromBigInt(valuespb.NewIntFromBigInt(rawDecimal.Coefficient), rawDecimal.Exponent), nil
}

func (e *EVM) readProto(ctx context.Context, request ctypes.Request, into proto.Message) (err error) {
	data, err := readType[[]byte](ctx, e.ConsensusHandler, request)
	if err != nil {
		return err
	}
	return proto.Unmarshal(data, into)
}

func readType[T any](ctx context.Context, reader ConsensusHandler, request ctypes.Request) (T, error) {
	var zero T
	resultCh, err := reader.Handle(ctx, request)
	if err != nil {
		return zero, err
	}

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case reply := <-resultCh:
		if reply.Err != nil {
			return zero, reply.Err
		}
		data, ok := reply.Value.(T)
		if !ok {
			return zero, fmt.Errorf("unexpected result type: expected %T, got %T", zero, reply.Value)
		}

		return data, nil
	}
}

func (e *EVM) isUserError(err error) bool {
	return !errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, multinode.ErrNodeError)
}

func GetError(err error, isUserError bool) caperrors.Error {
	if isUserError {
		return NewUserError(err)
	}
	return caperrors.NewPublicSystemError(err, caperrors.Unknown)
}

func NewUserError(err error) caperrors.Error {
	return caperrors.NewPublicUserError(err, caperrors.Unknown)
}

func EnsureRemoteReportable(err error) caperrors.Error {
	if err == nil {
		return nil
	}

	// placeholder for https://smartcontract-it.atlassian.net/browse/CAPPL-1067
	// should uncomment below
	//var targetUser *capabilities.RemoteReportableUserError
	//if errors.As(err, &targetUser) {
	//	return err
	//}
	var targetInternal caperrors.Error
	if errors.As(err, &targetInternal) {
		return targetInternal
	}

	// Not already remote-reportable -> wrap it.
	return caperrors.NewPublicSystemError(err, caperrors.Unknown)
}
