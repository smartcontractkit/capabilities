package actions

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus"

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

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"
)

type EVM struct {
	types.EVMService
	consensusHandler         chainconsensus.RequestHandler
	chainSelector            uint64
	keystoneForwarderAddress common.Address
	forwarderClient          contracts.CREForwarderClient
	ReceiverGasMinimum       uint64
	LookbackBlocks           uint64

	lggr              logger.SugaredLogger
	beholderProcessor beholder.ProtoProcessor
	messageBuilder    *monitoring.MessageBuilder

	readPayloadSizeLimiter                     limits.BoundLimiter[commoncfg.Size]
	logQueryBlockLimit                         limits.BoundLimiter[uint64]
	reportSizeLimit                            limits.BoundLimiter[commoncfg.Size]
	txGasLimit                                 limits.BoundLimiter[uint64]
	featureChainCapabilityHashBasedOCRActiveAt limits.RangeLimiter[commoncfg.Timestamp]
	writeReportL1FeeActive                     limits.RangeLimiter[commoncfg.Timestamp]

	transmissionScheduler ts.TransmissionScheduler
}

func NewEVM(cfg config.Config, evmService types.EVMService, lggr logger.Logger, beholderProcessor beholder.ProtoProcessor,
	messageBuilder *monitoring.MessageBuilder, handler chainconsensus.RequestHandler, chainSelector uint64, limitsFactory limits.Factory, transmissionScheduler ts.TransmissionScheduler) (*EVM, caperrors.Error) {
	keystoneForwarderAddress := common.HexToAddress(cfg.CREForwarderAddress)
	if keystoneForwarderAddress == (common.Address{}) {
		return &EVM{}, caperrors.NewPublicSystemError(errors.New("keystone forwarder address is not set"), caperrors.FailedPrecondition)
	}

	kfc, err := contracts.NewCREForwarderClient(evmService, keystoneForwarderAddress, cfg.ForwarderLookbackBlocks, lggr)
	if err != nil {
		return &EVM{}, caperrors.NewPublicSystemError(err, caperrors.Internal)
	}

	e := &EVM{
		EVMService:               evmService,
		keystoneForwarderAddress: keystoneForwarderAddress,
		forwarderClient:          kfc,
		ReceiverGasMinimum:       cfg.ReceiverGasMinimum,
		lggr:                     logger.Sugared(lggr),
		beholderProcessor:        beholderProcessor,
		messageBuilder:           messageBuilder,
		consensusHandler:         handler,
		chainSelector:            chainSelector,
		transmissionScheduler:    transmissionScheduler,
	}
	err = e.initLimiters(limitsFactory)
	if err != nil {
		return e, caperrors.NewPublicSystemError(err, caperrors.Internal)
	}

	return e, nil
}

func (e *EVM) initLimiters(limitsFactory limits.Factory) (err error) {
	e.readPayloadSizeLimiter, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainRead.PayloadSizeLimit)
	if err != nil {
		return
	}
	e.logQueryBlockLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainRead.LogQueryBlockLimit)
	if err != nil {
		return
	}
	e.reportSizeLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainWrite.ReportSizeLimit)
	if err != nil {
		return
	}
	e.txGasLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainWrite.EVM.GasLimit)
	if err != nil {
		return
	}
	e.featureChainCapabilityHashBasedOCRActiveAt, err = limits.MakeRangeLimiter(limitsFactory, cresettings.Default.PerWorkflow.FeatureChainCapabilityHashBasedOCRActivePeriod)
	if err != nil {
		return
	}
	e.writeReportL1FeeActive, err = limits.MakeRangeLimiter(limitsFactory, cresettings.Default.PerWorkflow.FeatureEVMWriteReportL1FeeActivePeriod)
	return
}

func (e *EVM) Close() error {
	return services.CloseAll(e.readPayloadSizeLimiter, e.logQueryBlockLimit, e.reportSizeLimit, e.txGasLimit, e.featureChainCapabilityHashBasedOCRActiveAt, e.writeReportL1FeeActive)
}

func requestID(meta capabilities.RequestMetadata) string {
	return commonMon.RequestID(meta.WorkflowExecutionID, meta.ReferenceID)
}

// useHashBasedConsensus is true when hash-based OCR (V2) should be used for reads that support it.
func (e *EVM) useHashBasedConsensus(ctx context.Context, meta capabilities.RequestMetadata) bool {
	return e.featureChainCapabilityHashBasedOCRActiveAt.Check(ctx, commoncfg.NewTimestamp(meta.ExecutionTimestamp)) == nil
}

func (e *EVM) CallContract(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	input *evm.CallContractRequest,
) (*capabilities.ResponseAndMetadata[*evm.CallContractReply], caperrors.Error) {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}

	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.CallContract)); err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.LimitExceeded)
	}

	callMsg, err := evm.ConvertCallMsgFromProto(input.GetCall())
	if err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
	}

	blockNumber, needsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(input.GetBlockNumber())
	if err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractInitiated(telemetryContext, callMsg, blockNumber.Int64()))

	callContract := func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
		callBlockNumber, err := getCallBlockNumber(blockNumber, height)
		if err != nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("error getting call block number: %w", err), caperrors.Internal)
		}

		resp, err := e.EVMService.CallContract(ctx, evmtypes.CallContractRequest{
			Msg:             callMsg,
			BlockNumber:     callBlockNumber,
			ConfidenceLevel: confidenceLevel,
			IsExternal:      true,
		})
		if err != nil {
			return nil, err
		}
		return resp.Data, nil
	}

	var responseAndMetadata *capabilities.ResponseAndMetadata[*evm.CallContractReply]
	if e.useHashBasedConsensus(ctx, meta) {
		responseAndMetadata, err = e.callContractV2(ctx, meta, needsBlockHeightConsensus, callContract)
	} else {
		responseAndMetadata, err = e.callContractV1(ctx, meta, needsBlockHeightConsensus, callContract)
	}

	if err != nil {
		capError := e.ensureCapabilityError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildCallContractError(telemetryContext, callMsg, blockNumber.Int64(), "Failed to read CallContract", capError))
		return nil, capError
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read CallContract", e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractSuccess(telemetryContext, callMsg, blockNumber.Int64()))
	return responseAndMetadata, nil
}

func (e *EVM) callContractV2(ctx context.Context, meta capabilities.RequestMetadata, needsBlockHeightConsensus bool,
	callContract func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error)) (*capabilities.ResponseAndMetadata[*evm.CallContractReply], error) {
	observe := func(ctx context.Context, height *ctypes.ChainHeight) (*evm.CallContractReply, error) {
		data, err := callContract(ctx, height)
		if err != nil {
			return nil, err
		}

		return &evm.CallContractReply{Data: data}, nil
	}

	metadata := metering.GetResponseMetadata(metering.CallContract)
	var request ctypes.HashableRequest[*evm.CallContractReply]
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockHashableRequest(meta.WorkflowExecutionID, meta.ReferenceID, metadata, observe)
	} else {
		request = ctypes.NewECHashableRequest(meta.WorkflowExecutionID, meta.ReferenceID, metadata, func(ctx context.Context) (*evm.CallContractReply, error) {
			return observe(ctx, nil)
		})
	}

	return chainconsensus.ReadHashableRequestReport(ctx, e.consensusHandler, request)
}

func (e *EVM) callContractV1(ctx context.Context, meta capabilities.RequestMetadata, needsBlockHeightConsensus bool,
	callContract func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error)) (*capabilities.ResponseAndMetadata[*evm.CallContractReply], error) {
	var request ctypes.Request
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID(meta), callContract)
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return callContract(ctx, nil)
		})
	}

	data, err := chainconsensus.ReadType[[]byte](ctx, e.consensusHandler, request)
	if err != nil {
		return nil, err
	}
	return &capabilities.ResponseAndMetadata[*evm.CallContractReply]{
		Response:         &evm.CallContractReply{Data: data},
		ResponseMetadata: metering.GetResponseMetadata(metering.CallContract),
	}, nil
}

type filterLogsQuery struct {
	EthFilterLogs             evmtypes.FilterQuery
	NormalizedFromBlock       rpc.BlockNumber
	NormalizedToBlock         rpc.BlockNumber
	NeedsBlockHeightConsensus bool
	ConfidenceLevel           primitives.ConfidenceLevel
}

func (e *EVM) convertLogsFilterFromProto(ctx context.Context, req *evm.FilterLogsRequest) (*filterLogsQuery, caperrors.Error) {
	ethFilterQuery, err := evm.ConvertFilterFromProto(req.GetFilterQuery())
	if err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
	}

	if ethFilterQuery.BlockHash != (evmtypes.Hash{}) {
		if ethFilterQuery.FromBlock != nil || ethFilterQuery.ToBlock != nil {
			return nil, caperrors.NewPublicUserError(errors.New("cannot specify both block hash and block range"), caperrors.InvalidArgument)
		}

		return &filterLogsQuery{
			EthFilterLogs:             ethFilterQuery,
			NeedsBlockHeightConsensus: false,
			ConfidenceLevel:           primitives.Unconfirmed,
		}, nil
	}

	fromBlock, fromNeedsBlockHeightConsensus, _, err := normalizeBlockNumber(valuespb.NewBigIntFromInt(ethFilterQuery.FromBlock))
	if err != nil {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("fromBlock is invalid: %w", err), caperrors.InvalidArgument)
	}

	toBlock, toNeedsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(valuespb.NewBigIntFromInt(ethFilterQuery.ToBlock))
	if err != nil {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("toBlock is invalid: %w", err), caperrors.InvalidArgument)
	}

	if !fromNeedsBlockHeightConsensus && !toNeedsBlockHeightConsensus {
		if err := e.validateBlockRangeInput(ctx, ethFilterQuery.FromBlock, ethFilterQuery.ToBlock); err != nil {
			return nil, err
		}

		return &filterLogsQuery{
			EthFilterLogs:             ethFilterQuery,
			NeedsBlockHeightConsensus: false,
			ConfidenceLevel:           primitives.Unconfirmed,
		}, nil
	}

	return &filterLogsQuery{
		EthFilterLogs:             ethFilterQuery,
		NormalizedFromBlock:       fromBlock,
		NormalizedToBlock:         toBlock,
		NeedsBlockHeightConsensus: true,
		ConfidenceLevel:           confidenceLevel,
	}, nil
}

func (e *EVM) validateBlockRangeInput(ctx context.Context, fromBlock, toBlock *big.Int) caperrors.Error {
	rangeSize := big.NewInt(0).Sub(toBlock, fromBlock)
	if rangeSize.Sign() < 0 {
		return caperrors.NewPublicUserError(fmt.Errorf("toBlock %s is less than fromBlock %s", toBlock.String(), fromBlock.String()), caperrors.InvalidArgument)
	}

	if !rangeSize.IsUint64() {
		return caperrors.NewPublicUserError(fmt.Errorf("block range size %s overflows uint64", rangeSize), caperrors.InvalidArgument)
	}

	if err := e.logQueryBlockLimit.Check(ctx, rangeSize.Uint64()); err != nil {
		return caperrors.NewPublicUserError(err, caperrors.LimitExceeded)
	}
	return nil
}

func (e *EVM) getLockedFilterLogsQuery(ctx context.Context, query *filterLogsQuery, height *ctypes.ChainHeight) (evmtypes.FilterQuery, primitives.ConfidenceLevel, error) {
	callFromBlock, err := getCallBlockNumber(query.NormalizedFromBlock, height)
	if err != nil {
		return evmtypes.FilterQuery{}, primitives.Unconfirmed, caperrors.NewPublicSystemError(fmt.Errorf("error getting callFromBlock: %w", err), caperrors.Internal)
	}

	callToBlock, err := getCallBlockNumber(query.NormalizedToBlock, height)
	if err != nil {
		return evmtypes.FilterQuery{}, primitives.Unconfirmed, caperrors.NewPublicSystemError(fmt.Errorf("error getting callToBlock: %w", err), caperrors.Internal)
	}

	if err := e.validateBlockRangeInput(ctx, callFromBlock, callToBlock); err != nil {
		return evmtypes.FilterQuery{}, primitives.Unconfirmed, err
	}

	result := query.EthFilterLogs // copy
	result.FromBlock = big.NewInt(callFromBlock.Int64())
	result.ToBlock = big.NewInt(callToBlock.Int64())
	return result, query.ConfidenceLevel, nil
}

func (e *EVM) filterLogsObserve(ctx context.Context, query evmtypes.FilterQuery, confidenceLevel primitives.ConfidenceLevel) (*evm.FilterLogsReply, []byte, error) {
	serviceReply, err := e.EVMService.FilterLogs(ctx, evmtypes.FilterLogsRequest{
		FilterQuery:     query,
		ConfidenceLevel: confidenceLevel,
		IsExternal:      true,
	})
	if err != nil {
		return nil, nil, err
	}

	logs, err := evm.ConvertLogsToProto(serviceReply.Logs)
	if err != nil {
		return nil, nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to convert logs to proto: %w", err), caperrors.Internal)
	}

	capReply := &evm.FilterLogsReply{Logs: logs}
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(capReply)
	if err != nil {
		return nil, nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to marshal filter logs reply: %w", err), caperrors.Internal)
	}
	if err = e.readPayloadSizeLimiter.Check(ctx, commoncfg.SizeOf(b)); err != nil {
		return nil, nil, caperrors.NewPublicUserError(err, caperrors.LimitExceeded)
	}
	return capReply, b, nil
}

func (e *EVM) filterLogsV1(ctx context.Context, requestID string, query *filterLogsQuery) (*capabilities.ResponseAndMetadata[*evm.FilterLogsReply], error) {
	var request ctypes.Request
	if query.NeedsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID, func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
			lockedQuery, confidenceLevel, err := e.getLockedFilterLogsQuery(ctx, query, height)
			if err != nil {
				return nil, err
			}

			_, asBytes, err := e.filterLogsObserve(ctx, lockedQuery, confidenceLevel)
			return asBytes, err
		})
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID, func(ctx context.Context) ([]byte, error) {
			_, asBytes, err := e.filterLogsObserve(ctx, query.EthFilterLogs, query.ConfidenceLevel)
			return asBytes, err
		})
	}

	var reply evm.FilterLogsReply
	if err := e.readProto(ctx, request, &reply); err != nil {
		return nil, err
	}
	return &capabilities.ResponseAndMetadata[*evm.FilterLogsReply]{
		Response:         &reply,
		ResponseMetadata: metering.GetResponseMetadata(metering.FilterLogs),
	}, nil
}

func (e *EVM) filterLogsV2(ctx context.Context, meta capabilities.RequestMetadata, query *filterLogsQuery) (*capabilities.ResponseAndMetadata[*evm.FilterLogsReply], error) {
	var request ctypes.HashableRequest[*evm.FilterLogsReply]
	if query.NeedsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockHashableRequest(meta.WorkflowExecutionID, meta.ReferenceID, metering.GetResponseMetadata(metering.FilterLogs), func(ctx context.Context, height *ctypes.ChainHeight) (*evm.FilterLogsReply, error) {
			lockedQuery, confidenceLevel, err := e.getLockedFilterLogsQuery(ctx, query, height)
			if err != nil {
				return nil, err
			}

			result, _, err := e.filterLogsObserve(ctx, lockedQuery, confidenceLevel)
			return result, err
		})
	} else {
		request = ctypes.NewECHashableRequest(meta.WorkflowExecutionID, meta.ReferenceID, metering.GetResponseMetadata(metering.FilterLogs), func(ctx context.Context) (*evm.FilterLogsReply, error) {
			result, _, err := e.filterLogsObserve(ctx, query.EthFilterLogs, query.ConfidenceLevel)
			return result, err
		})
	}

	return chainconsensus.ReadHashableRequestReport(ctx, e.consensusHandler, request)
}

func (e *EVM) FilterLogs(ctx context.Context, meta capabilities.RequestMetadata, req *evm.FilterLogsRequest) (*capabilities.ResponseAndMetadata[*evm.FilterLogsReply], caperrors.Error) {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}

	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.FilterLogs)); err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.LimitExceeded)
	}

	query, capError := e.convertLogsFilterFromProto(ctx, req)
	if capError != nil {
		return nil, capError
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsInitiated(telemetryContext, query.EthFilterLogs))

	var responseAndMetadata *capabilities.ResponseAndMetadata[*evm.FilterLogsReply]
	var err error
	if e.useHashBasedConsensus(ctx, meta) {
		responseAndMetadata, err = e.filterLogsV2(ctx, meta, query)
	} else {
		responseAndMetadata, err = e.filterLogsV1(ctx, requestID(meta), query)
	}
	if err != nil {
		capError := e.ensureCapabilityError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildFilterLogsError(telemetryContext, query.EthFilterLogs, "Failed to FilterLogs", capError))
		return nil, capError
	}

	// G115: integer overflow conversion int -> int32 (gosec)
	// nolint:gosec
	monitoring.LogAndEmitSuccess(ctx, "Successfully executed FilterLogs", e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsSuccess(telemetryContext, query.EthFilterLogs, int32(len(responseAndMetadata.Response.Logs))))
	return responseAndMetadata, nil
}

func (e *EVM) balanceAtV1(ctx context.Context, requestID string, needsBlockHeightConsensus bool, balanceAt func(ctx context.Context, height *ctypes.ChainHeight) (*evm.BalanceAtReply, error)) (*capabilities.ResponseAndMetadata[*evm.BalanceAtReply], error) {
	byteObserve := func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
		r, err := balanceAt(ctx, height)
		if err != nil {
			return nil, err
		}
		balance, err := proto.Marshal(r.Balance)
		if err != nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to marshal balance reply: %w", err), caperrors.Internal)
		}
		return balance, nil
	}
	var request ctypes.Request
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID, byteObserve)
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID, func(ctx context.Context) ([]byte, error) {
			return byteObserve(ctx, nil)
		})
	}

	balance := new(valuespb.BigInt)
	if err := e.readProto(ctx, request, balance); err != nil {
		return nil, err
	}
	return &capabilities.ResponseAndMetadata[*evm.BalanceAtReply]{
		Response:         &evm.BalanceAtReply{Balance: balance},
		ResponseMetadata: metering.GetResponseMetadata(metering.BalanceAt),
	}, nil
}

func (e *EVM) balanceAtV2(ctx context.Context, meta capabilities.RequestMetadata, needsBlockHeightConsensus bool,
	balanceAt func(ctx context.Context, height *ctypes.ChainHeight) (*evm.BalanceAtReply, error)) (*capabilities.ResponseAndMetadata[*evm.BalanceAtReply], error) {
	var request ctypes.HashableRequest[*evm.BalanceAtReply]
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockHashableRequest(meta.WorkflowExecutionID, meta.ReferenceID, metering.GetResponseMetadata(metering.BalanceAt), balanceAt)
	} else {
		request = ctypes.NewECHashableRequest(meta.WorkflowExecutionID, meta.ReferenceID, metering.GetResponseMetadata(metering.BalanceAt), func(ctx context.Context) (*evm.BalanceAtReply, error) {
			return balanceAt(ctx, nil)
		})
	}

	return chainconsensus.ReadHashableRequestReport(ctx, e.consensusHandler, request)
}

func (e *EVM) BalanceAt(ctx context.Context, meta capabilities.RequestMetadata, req *evm.BalanceAtRequest) (*capabilities.ResponseAndMetadata[*evm.BalanceAtReply], caperrors.Error) {
	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.BalanceAt)); err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.LimitExceeded)
	}
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	blockNumber, needsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(req.GetBlockNumber())
	if err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildBalanceAtInitiated(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64()))

	balanceAt := func(ctx context.Context, height *ctypes.ChainHeight) (*evm.BalanceAtReply, error) {
		callBlockNumber, err := getCallBlockNumber(blockNumber, height)
		if err != nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("error getting call block number: %w", err), caperrors.Internal)
		}

		address, err := evmservice.ConvertOptionalAddressFromProto(req.GetAccount())
		if err != nil {
			return nil, caperrors.NewPublicUserError(fmt.Errorf("error converting address from proto: %w", err), caperrors.InvalidArgument)
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
		return &evm.BalanceAtReply{Balance: pbBalance}, nil
	}

	var responseAndMetadata *capabilities.ResponseAndMetadata[*evm.BalanceAtReply]
	if e.useHashBasedConsensus(ctx, meta) {
		responseAndMetadata, err = e.balanceAtV2(ctx, meta, needsBlockHeightConsensus, balanceAt)
	} else {
		responseAndMetadata, err = e.balanceAtV1(ctx, requestID(meta), needsBlockHeightConsensus, balanceAt)
	}
	if err != nil {
		capError := e.ensureCapabilityError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildBalanceAtError(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64(), "Failed to read BalanceAt", capError))
		return nil, capError
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read BalanceAt", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildBalanceAtSuccess(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64(), valuespb.NewIntFromBigInt(responseAndMetadata.Response.Balance)))
	return responseAndMetadata, nil
}

func (e *EVM) EstimateGas(ctx context.Context, meta capabilities.RequestMetadata, req *evm.EstimateGasRequest) (*capabilities.ResponseAndMetadata[*evm.EstimateGasReply], caperrors.Error) {
	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.EstimateGas)); err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.LimitExceeded)
	}
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	msg, err := evm.ConvertCallMsgFromProto(req.GetMsg())
	if err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
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

	rawEstimate, err := chainconsensus.ReadDecimal(ctx, e.consensusHandler, request)
	if err != nil {
		capError := e.ensureCapabilityError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildEstimateGasError(telemetryContext, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, "Failed to execute EstimateGas", capError))
		return nil, capError
	}

	logMsg := e.messageBuilder.BuildEstimateGasSuccess(telemetryContext, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, rawEstimate.BigInt().Int64())
	monitoring.LogAndEmitSuccess(ctx, "Successfully read EstimateGas", e.lggr, e.beholderProcessor, logMsg)
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.EstimateGasReply]{
		Response:         &evm.EstimateGasReply{Gas: rawEstimate.BigInt().Uint64()},
		ResponseMetadata: metering.GetResponseMetadata(metering.EstimateGas),
	}
	return &responseAndMetadata, nil
}

func (e *EVM) getTransactionByHashObserve(ctx context.Context, hash common.Hash) (*evm.GetTransactionByHashReply, error) {
	tx, err := e.EVMService.GetTransactionByHash(ctx, evmtypes.GetTransactionByHashRequest{
		Hash:       hash,
		IsExternal: true,
	})
	if err != nil {
		return nil, err
	}

	protoTx, err := evm.ConvertTransactionToProto(tx)
	if err != nil {
		return nil, caperrors.NewPublicSystemError(err, caperrors.Internal)
	}

	return &evm.GetTransactionByHashReply{Transaction: protoTx}, nil
}

func (e *EVM) getTransactionByHashV1(ctx context.Context, requestID string, hash common.Hash) (*capabilities.ResponseAndMetadata[*evm.GetTransactionByHashReply], error) {
	request := ctypes.NewEventuallyConsistentRequest(requestID, func(ctx context.Context) ([]byte, error) {
		r, err := e.getTransactionByHashObserve(ctx, hash)
		if err != nil {
			return nil, err
		}
		tx, err := proto.MarshalOptions{Deterministic: true}.Marshal(r.Transaction)
		if err != nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to marshal transaction reply: %w", err), caperrors.Internal)
		}
		return tx, nil
	})

	var tx evm.Transaction
	if err := e.readProto(ctx, request, &tx); err != nil {
		return nil, err
	}
	return &capabilities.ResponseAndMetadata[*evm.GetTransactionByHashReply]{
		Response:         &evm.GetTransactionByHashReply{Transaction: &tx},
		ResponseMetadata: metering.GetResponseMetadata(metering.GetTransactionByHash),
	}, nil
}

func (e *EVM) getTransactionByHashV2(ctx context.Context, meta capabilities.RequestMetadata, hash common.Hash) (*capabilities.ResponseAndMetadata[*evm.GetTransactionByHashReply], error) {
	request := ctypes.NewECHashableRequest(meta.WorkflowExecutionID, meta.ReferenceID, metering.GetResponseMetadata(metering.GetTransactionByHash), func(ctx context.Context) (*evm.GetTransactionByHashReply, error) {
		return e.getTransactionByHashObserve(ctx, hash)
	})
	return chainconsensus.ReadHashableRequestReport[*evm.GetTransactionByHashReply](ctx, e.consensusHandler, request)
}

func (e *EVM) GetTransactionByHash(ctx context.Context, meta capabilities.RequestMetadata, req *evm.GetTransactionByHashRequest) (*capabilities.ResponseAndMetadata[*evm.GetTransactionByHashReply], caperrors.Error) {
	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.GetTransactionByHash)); err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.LimitExceeded)
	}
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	hash, err := evmservice.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionByHashInitiated(telemetryContext, common.Bytes2Hex(hash[:])))

	var responseAndMetadata *capabilities.ResponseAndMetadata[*evm.GetTransactionByHashReply]
	if e.useHashBasedConsensus(ctx, meta) {
		responseAndMetadata, err = e.getTransactionByHashV2(ctx, meta, hash)
	} else {
		responseAndMetadata, err = e.getTransactionByHashV1(ctx, requestID(meta), hash)
	}
	if err != nil {
		capError := e.ensureCapabilityError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildGetTransactionByHashError(telemetryContext, common.Bytes2Hex(hash[:]), "Failed to execute GetTransactionByHash", capError))
		return nil, capError
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read GetTransactionByHash", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildGetTransactionByHashSuccess(telemetryContext, common.Bytes2Hex(hash[:]), responseAndMetadata.Response.Transaction))
	return responseAndMetadata, nil
}

func (e *EVM) getTransactionReceiptObserve(ctx context.Context, hash common.Hash) (*evm.GetTransactionReceiptReply, error) {
	receipt, err := e.EVMService.GetTransactionReceipt(ctx, evmtypes.GeTransactionReceiptRequest{
		Hash:       hash,
		IsExternal: true,
	})
	if err != nil {
		return nil, err
	}

	protoReceipt, err := evm.ConvertReceiptToProto(receipt)
	if err != nil {
		return nil, caperrors.NewPublicSystemError(err, caperrors.Internal)
	}

	return &evm.GetTransactionReceiptReply{Receipt: protoReceipt}, nil
}

func (e *EVM) getTransactionReceiptV1(ctx context.Context, requestID string, hash common.Hash) (*capabilities.ResponseAndMetadata[*evm.GetTransactionReceiptReply], error) {
	request := ctypes.NewEventuallyConsistentRequest(requestID, func(ctx context.Context) ([]byte, error) {
		r, err := e.getTransactionReceiptObserve(ctx, hash)
		if err != nil {
			return nil, err
		}
		receipt, err := proto.MarshalOptions{Deterministic: true}.Marshal(r.Receipt)
		if err != nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to marshal receipt reply: %w", err), caperrors.Internal)
		}
		return receipt, nil
	})

	var receipt evm.Receipt
	if err := e.readProto(ctx, request, &receipt); err != nil {
		return nil, err
	}
	return &capabilities.ResponseAndMetadata[*evm.GetTransactionReceiptReply]{
		Response:         &evm.GetTransactionReceiptReply{Receipt: &receipt},
		ResponseMetadata: metering.GetResponseMetadata(metering.GetTransactionReceipt),
	}, nil
}

func (e *EVM) getTransactionReceiptV2(ctx context.Context, meta capabilities.RequestMetadata, hash common.Hash) (*capabilities.ResponseAndMetadata[*evm.GetTransactionReceiptReply], error) {
	request := ctypes.NewECHashableRequest(meta.WorkflowExecutionID, meta.ReferenceID, metering.GetResponseMetadata(metering.GetTransactionReceipt), func(ctx context.Context) (*evm.GetTransactionReceiptReply, error) {
		return e.getTransactionReceiptObserve(ctx, hash)
	})
	return chainconsensus.ReadHashableRequestReport[*evm.GetTransactionReceiptReply](ctx, e.consensusHandler, request)
}

func (e *EVM) GetTransactionReceipt(ctx context.Context, meta capabilities.RequestMetadata, req *evm.GetTransactionReceiptRequest) (*capabilities.ResponseAndMetadata[*evm.GetTransactionReceiptReply], caperrors.Error) {
	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.GetTransactionReceipt)); err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.LimitExceeded)
	}
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	hash, err := evmservice.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionReceiptInitiated(telemetryContext, common.Bytes2Hex(hash[:])))

	var responseAndMetadata *capabilities.ResponseAndMetadata[*evm.GetTransactionReceiptReply]
	if e.useHashBasedConsensus(ctx, meta) {
		responseAndMetadata, err = e.getTransactionReceiptV2(ctx, meta, hash)
	} else {
		responseAndMetadata, err = e.getTransactionReceiptV1(ctx, requestID(meta), hash)
	}
	if err != nil {
		capError := e.ensureCapabilityError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildGetTransactionReceiptError(telemetryContext, common.Bytes2Hex(hash[:]), "Failed to get latest and finalized head", capError))
		return nil, capError
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read GetTransactionReceiptSuccess", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildGetTransactionReceiptSuccess(telemetryContext, common.Bytes2Hex(hash[:]), responseAndMetadata.Response.Receipt))
	return responseAndMetadata, nil
}

func (e *EVM) headerByNumberV1(ctx context.Context, requestID string, needsBlockHeightConsensus bool, headerByNumber func(ctx context.Context, height *ctypes.ChainHeight) (*evm.HeaderByNumberReply, error)) (*capabilities.ResponseAndMetadata[*evm.HeaderByNumberReply], error) {
	byteObserve := func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
		r, err := headerByNumber(ctx, height)
		if err != nil {
			return nil, err
		}
		header, err := proto.MarshalOptions{Deterministic: true}.Marshal(r)
		if err != nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to marshal header reply: %w", err), caperrors.Internal)
		}
		return header, nil
	}
	var request ctypes.Request
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID, byteObserve)
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID, func(ctx context.Context) ([]byte, error) {
			return byteObserve(ctx, nil)
		})
	}

	var reply evm.HeaderByNumberReply
	if err := e.readProto(ctx, request, &reply); err != nil {
		return nil, err
	}
	return &capabilities.ResponseAndMetadata[*evm.HeaderByNumberReply]{
		Response:         &reply,
		ResponseMetadata: metering.GetResponseMetadata(metering.HeaderByNumber),
	}, nil
}

func (e *EVM) headerByNumberV2(ctx context.Context, meta capabilities.RequestMetadata, needsBlockHeightConsensus bool,
	headerByNumber func(ctx context.Context, height *ctypes.ChainHeight) (*evm.HeaderByNumberReply, error)) (*capabilities.ResponseAndMetadata[*evm.HeaderByNumberReply], error) {
	var request ctypes.HashableRequest[*evm.HeaderByNumberReply]
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockHashableRequest(meta.WorkflowExecutionID, meta.ReferenceID, metering.GetResponseMetadata(metering.GetTransactionReceipt), headerByNumber)
	} else {
		request = ctypes.NewECHashableRequest(meta.WorkflowExecutionID, meta.ReferenceID, metering.GetResponseMetadata(metering.GetTransactionReceipt), func(ctx context.Context) (*evm.HeaderByNumberReply, error) {
			return headerByNumber(ctx, nil)
		})
	}

	return chainconsensus.ReadHashableRequestReport(ctx, e.consensusHandler, request)
}

func (e *EVM) HeaderByNumber(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	req *evm.HeaderByNumberRequest,
) (*capabilities.ResponseAndMetadata[*evm.HeaderByNumberReply], caperrors.Error) {
	if err := metering.CheckHasFunds(e.lggr, meta, metering.ActionSpendUnit, string(metering.HeaderByNumber)); err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.LimitExceeded)
	}
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	blockNumber, needsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(req.GetBlockNumber())
	if err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildHeaderByNumberInitiated(telemetryContext, blockNumber.Int64()))

	headerByNumber := func(ctx context.Context, height *ctypes.ChainHeight) (*evm.HeaderByNumberReply, error) {
		callBlockNumber, err := getCallBlockNumber(blockNumber, height)
		if err != nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("error getting call block number: %w", err), caperrors.Internal)
		}

		reply, err := e.EVMService.HeaderByNumber(ctx, evmtypes.HeaderByNumberRequest{
			Number:          callBlockNumber,
			ConfidenceLevel: confidenceLevel,
		})
		if err != nil {
			return nil, err
		}

		if reply.Header == nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("header is nil"), caperrors.Internal)
		}

		header, err := evm.ConvertHeaderToProto(reply.Header)
		if err != nil {
			return nil, caperrors.NewPublicSystemError(err, caperrors.Internal)
		}

		return &evm.HeaderByNumberReply{Header: header}, nil
	}

	var responseAndMetadata *capabilities.ResponseAndMetadata[*evm.HeaderByNumberReply]
	if e.useHashBasedConsensus(ctx, meta) {
		responseAndMetadata, err = e.headerByNumberV2(ctx, meta, needsBlockHeightConsensus, headerByNumber)
	} else {
		responseAndMetadata, err = e.headerByNumberV1(ctx, requestID(meta), needsBlockHeightConsensus, headerByNumber)
	}
	if err != nil {
		capError := e.ensureCapabilityError(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildHeaderByNumberError(telemetryContext, blockNumber.Int64(), "Failed to get header by number", capError))
		return nil, capError
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully got header by number", e.lggr, e.beholderProcessor, e.messageBuilder.BuildHeaderByNumberSuccess(telemetryContext, blockNumber.Int64(), responseAndMetadata.Response.Header))
	return responseAndMetadata, nil
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

func (e *EVM) readProto(ctx context.Context, request ctypes.Request, into proto.Message) (err error) {
	data, err := chainconsensus.ReadType[[]byte](ctx, e.consensusHandler, request)
	if err != nil {
		return err
	}
	if err := proto.Unmarshal(data, into); err != nil {
		return caperrors.NewPublicSystemError(fmt.Errorf("failed to unmarshal response proto: %w", err), caperrors.Internal)
	}
	return nil
}

func (e *EVM) isUserError(err error) bool {
	return !errors.Is(err, context.DeadlineExceeded) && !isEVMNodeInfraError(err)
}

func isEVMNodeInfraError(err error) bool {
	return errors.Is(err, multinode.ErrNodeError) ||
		strings.Contains(err.Error(), multinode.ErrNodeError.Error())
}

// ensureCapabilityError is meant to be used as a safeguard to guarantee all actions' error responses are of type caperror.Error,
// so the platform can properly handle them in the WF DON side of it
func (e *EVM) ensureCapabilityError(err error) caperrors.Error {
	if capErr, ok := errors.AsType[caperrors.Error](err); ok {
		return capErr
	}
	// Should only reach up here for legacy paths that still return plain unhandled errors, and those should always be a system one
	isUserError := e.isUserError(err)
	if isUserError {
		e.lggr.Errorw("Got a user error in the last safety check, this should have been caught earlier", "error", err.Error())
	}
	return capcommon.GetError(err, isUserError)
}
