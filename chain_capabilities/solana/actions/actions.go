package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/metering"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	capmon "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/monitoring"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-framework/multinode"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
)

type Solana struct {
	types.SolanaService
	readsEnabled             bool
	forwarderClient          CREForwarderClient
	transmissionInfoProvider TransmissionInfoProvider
	lggr                     logger.SugaredLogger
	chainSelector            uint64
	txComputeLimit           limits.BoundLimiter[uint32]
	reportSizeLimit          limits.BoundLimiter[commoncfg.Size]
	readPayloadSizeLimiter   limits.BoundLimiter[commoncfg.Size]
	batchItemLimit           limits.BoundLimiter[int]
	beholderProcessor        beholder.ProtoProcessor
	messageBuilder           *monitoring.MessageBuilder
	transmissionScheduler    ts.TransmissionScheduler
	handler                  chainconsensus.RequestHandler
}

func NewSolana(ctx context.Context, cfg *config.Config, s types.SolanaService, messageBuilder *monitoring.MessageBuilder,
	beholderProcessor beholder.ProtoProcessor, lggr logger.Logger, limitsFactory limits.Factory,
	transmissionScheduler ts.TransmissionScheduler, chainSelector uint64,
	handler chainconsensus.RequestHandler,
) (*Solana, error) {
	client := newForwarderClient(s, lggr, cfg.CREForwarderAddress, cfg.CREForwarderState, cfg.Transmitter)
	provider, err := newOnChainTransmissionInfoProvider(ctx, cfg.CREForwarderAddress, cfg.CREForwarderState, s)
	if err != nil {
		return nil, fmt.Errorf("failed to create on-chain transmission info provider: %w", err)
	}
	sol := &Solana{
		readsEnabled:             cfg.ReadsEnabled,
		SolanaService:            s,
		chainSelector:            chainSelector,
		lggr:                     logger.Sugared(lggr),
		forwarderClient:          client,
		transmissionInfoProvider: provider,
		messageBuilder:           messageBuilder,
		beholderProcessor:        beholderProcessor,
		transmissionScheduler:    transmissionScheduler,
		handler:                  handler,
	}

	return sol, sol.initLimiters(limitsFactory)
}

func (s *Solana) GetAccountInfoWithOpts(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetAccountInfoWithOptsRequest) (*capabilities.ResponseAndMetadata[*solcap.GetAccountInfoWithOptsReply], caperrors.Error) {
	if !s.readsEnabled {
		return nil, caperrors.NewPublicSystemError(errors.New("reads are not available"), caperrors.Internal)
	}
	// TODO: implement billing once generalized PLEX-3022
	request, err := solcap.ConvertGetAccountInfoRequestFromProto(input)
	if err != nil {
		return nil, NewUserError(fmt.Errorf("invalid request: %w", err))
	}
	request.IsExternal = true
	lggr := s.messageBuilder.RequestLggr(s.lggr, monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}).With("request", request)
	cReq := ctypes.NewVolatileRequest(metadata.WorkflowExecutionID, metadata.ReferenceID, metering.GetResponseMetadata(metering.GetAccountInfo), func(ctx context.Context) (*solcap.GetAccountInfoWithOptsReply, uint64, error) {
		rawResponse, err := s.SolanaService.GetAccountInfoWithOpts(ctx, request)
		if err != nil {
			return nil, 0, err
		}

		response, err := solcap.ConvertGetAccountInfoReplyToProto(rawResponse)
		if err != nil {
			return nil, 0, caperrors.NewPublicSystemError(fmt.Errorf("failed to convert response to proto: %w", err), caperrors.Internal)
		}

		if err = s.checkReadPayloadSize(ctx, response); err != nil {
			return nil, 0, NewUserError(err)
		}
		return response, rawResponse.Slot, nil
	}, lggr)
	responseAndMetadata, err := chainconsensus.ReadHashableRequestReport[*solcap.GetAccountInfoWithOptsReply](ctx, s.handler, cReq)
	if err != nil {
		return nil, getReadError(lggr, fmt.Errorf("failed to GetAccountInfoWithOpts: %w", err))
	}

	return responseAndMetadata, nil
}

func (s *Solana) GetBalance(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetBalanceRequest) (*capabilities.ResponseAndMetadata[*solcap.GetBalanceReply], caperrors.Error) {
	if !s.readsEnabled {
		return nil, caperrors.NewPublicSystemError(errors.New("reads are not available"), caperrors.Internal)
	}
	request, err := solcap.ConvertGetBalanceRequestFromProto(input)
	if err != nil {
		return nil, NewUserError(fmt.Errorf("invalid request: %w", err))
	}

	lggr := s.messageBuilder.RequestLggr(s.lggr, monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}).With("request", request)
	cReq := ctypes.NewVolatileRequest(metadata.WorkflowExecutionID, metadata.ReferenceID, metering.GetResponseMetadata(metering.GetAccountInfo), func(ctx context.Context) (*solcap.GetBalanceReply, uint64, error) {
		rawResponse, err := s.SolanaService.GetBalance(ctx, request)
		if err != nil {
			return nil, 0, err
		}

		response, err := solcap.ConvertGetBalanceReplyToProto(rawResponse)
		if err != nil {
			return nil, 0, caperrors.NewPublicSystemError(fmt.Errorf("failed to convert response to proto: %w", err), caperrors.Internal)
		}

		return response, 0, nil
	}, lggr)
	responseAndMetadata, err := chainconsensus.ReadHashableRequestReport[*solcap.GetBalanceReply](ctx, s.handler, cReq)
	if err != nil {
		return nil, getReadError(lggr, fmt.Errorf("failed to GetBalance: %w", err))
	}

	return responseAndMetadata, nil
}

func (s *Solana) GetBlock(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetBlockRequest) (*capabilities.ResponseAndMetadata[*solcap.GetBlockReply], caperrors.Error) {
	if !s.readsEnabled {
		return nil, caperrors.NewPublicSystemError(errors.New("reads are not available"), caperrors.Internal)
	}
	request, err := solcap.ConvertGetBlockRequestFromProto(input)
	if err != nil {
		return nil, NewUserError(fmt.Errorf("invalid request: %w", err))
	}

	lggr := s.messageBuilder.RequestLggr(s.lggr, monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}).With("request", request)
	cReq := ctypes.NewECHashableRequest(metadata.WorkflowExecutionID, metadata.ReferenceID, metering.GetResponseMetadata(metering.GetAccountInfo), func(ctx context.Context) (*solcap.GetBlockReply, error) {
		rawResponse, err := s.SolanaService.GetBlock(ctx, *request)
		if err != nil {
			return nil, err
		}

		response, err := solcap.ConvertGetBlockReplyToProto(rawResponse)
		if err != nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to convert response to proto: %w", err), caperrors.Internal)
		}

		if err = s.checkReadPayloadSize(ctx, response); err != nil {
			return nil, NewUserError(err)
		}
		return response, nil
	})
	responseAndMetadata, err := chainconsensus.ReadHashableRequestReport[*solcap.GetBlockReply](ctx, s.handler, cReq)
	if err != nil {
		return nil, getReadError(lggr, fmt.Errorf("failed to GetBlock: %w", err))
	}

	return responseAndMetadata, nil
}

func (s *Solana) GetFeeForMessage(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetFeeForMessageRequest) (*capabilities.ResponseAndMetadata[*solcap.GetFeeForMessageReply], caperrors.Error) {
	if !s.readsEnabled {
		return nil, caperrors.NewPublicSystemError(errors.New("reads are not available"), caperrors.Internal)
	}
	request, err := solcap.ConvertGetFeeForMessageRequestFromProto(input)
	if err != nil {
		return nil, NewUserError(fmt.Errorf("invalid request: %w", err))
	}

	lggr := s.messageBuilder.RequestLggr(s.lggr, monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}).With("request", request)
	cReq := ctypes.NewAggregatableRequest(commonMon.RequestID(metadata.WorkflowExecutionID, metadata.ReferenceID), func(ctx context.Context) (*ctypes.AggregatableObservation, error) {
		rawResponse, err := s.SolanaService.GetFeeForMessage(ctx, *request)
		if err != nil {
			return nil, err
		}

		return &ctypes.AggregatableObservation{
			Method: ctypes.AggregationMethodFPlusOneHighest,
			Value: &valuespb.Decimal{
				Coefficient: valuespb.NewBigIntFromInt(new(big.Int).SetUint64(rawResponse.Fee)),
				Exponent:    0,
			},
		}, nil
	})
	aggregatedFee, err := chainconsensus.ReadDecimal(ctx, s.handler, cReq)
	if err != nil {
		return nil, getReadError(lggr, fmt.Errorf("failed to GetFeeForMessage: %w", err))
	}

	return &capabilities.ResponseAndMetadata[*solcap.GetFeeForMessageReply]{
		Response:         &solcap.GetFeeForMessageReply{Fee: aggregatedFee.BigInt().Uint64()},
		ResponseMetadata: metering.GetResponseMetadata(metering.GetAccountInfo),
	}, nil
}

func (s *Solana) GetMultipleAccountsWithOpts(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetMultipleAccountsWithOptsRequest) (*capabilities.ResponseAndMetadata[*solcap.GetMultipleAccountsWithOptsReply], caperrors.Error) {
	if !s.readsEnabled {
		return nil, caperrors.NewPublicSystemError(errors.New("reads are not available"), caperrors.Internal)
	}
	request, err := solcap.ConvertGetMultipleAccountsRequestFromProto(input)
	if err != nil {
		return nil, NewUserError(fmt.Errorf("invalid request: %w", err))
	}
	if err := s.validateBatchItemCount(ctx, len(input.GetAccounts())); err != nil {
		return nil, NewUserError(err)
	}
	request.IsExternal = true
	lggr := s.messageBuilder.RequestLggr(s.lggr, monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}).With("request", request)
	cReq := ctypes.NewVolatileRequest(metadata.WorkflowExecutionID, metadata.ReferenceID, metering.GetResponseMetadata(metering.GetAccountInfo), func(ctx context.Context) (*solcap.GetMultipleAccountsWithOptsReply, uint64, error) {
		rawResponse, err := s.SolanaService.GetMultipleAccountsWithOpts(ctx, *request)
		if err != nil {
			return nil, 0, err
		}

		response, err := solcap.ConvertGetMultipleAccountsReplyToProto(rawResponse)
		if err != nil {
			return nil, 0, caperrors.NewPublicSystemError(fmt.Errorf("failed to convert response to proto: %w", err), caperrors.Internal)
		}

		if err = s.checkReadPayloadSize(ctx, response); err != nil {
			return nil, 0, NewUserError(err)
		}
		return response, rawResponse.Slot, nil
	}, lggr)
	responseAndMetadata, err := chainconsensus.ReadHashableRequestReport[*solcap.GetMultipleAccountsWithOptsReply](ctx, s.handler, cReq)
	if err != nil {
		return nil, getReadError(lggr, fmt.Errorf("failed to GetMultipleAccountsWithOpts: %w", err))
	}

	return responseAndMetadata, nil
}

func (s *Solana) GetSignatureStatuses(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetSignatureStatusesRequest) (*capabilities.ResponseAndMetadata[*solcap.GetSignatureStatusesReply], caperrors.Error) {
	if !s.readsEnabled {
		return nil, caperrors.NewPublicSystemError(errors.New("reads are not available"), caperrors.Internal)
	}
	request, err := solcap.ConvertGetSignatureStatusesRequestFromProto(input)
	if err != nil {
		return nil, NewUserError(fmt.Errorf("invalid request: %w", err))
	}
	if err := s.validateBatchItemCount(ctx, len(input.GetSigs())); err != nil {
		return nil, NewUserError(err)
	}

	lggr := s.messageBuilder.RequestLggr(s.lggr, monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}).With("request", request)
	cReq := ctypes.NewECHashableRequest(metadata.WorkflowExecutionID, metadata.ReferenceID, metering.GetResponseMetadata(metering.GetAccountInfo), func(ctx context.Context) (*solcap.GetSignatureStatusesReply, error) {
		rawResponse, err := s.SolanaService.GetSignatureStatuses(ctx, *request)
		if err != nil {
			return nil, err
		}

		response, err := solcap.ConvertGetSignatureStatusesReplyToProto(rawResponse)
		if err != nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to convert response to proto: %w", err), caperrors.Internal)
		}

		if err = s.checkReadPayloadSize(ctx, response); err != nil {
			return nil, NewUserError(err)
		}
		return response, nil
	})
	responseAndMetadata, err := chainconsensus.ReadHashableRequestReport[*solcap.GetSignatureStatusesReply](ctx, s.handler, cReq)
	if err != nil {
		return nil, getReadError(lggr, fmt.Errorf("failed to GetSignatureStatuses: %w", err))
	}

	return responseAndMetadata, nil
}

func (s *Solana) GetSlotHeight(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetSlotHeightRequest) (*capabilities.ResponseAndMetadata[*solcap.GetSlotHeightReply], caperrors.Error) {
	if !s.readsEnabled {
		return nil, caperrors.NewPublicSystemError(errors.New("reads are not available"), caperrors.Internal)
	}
	request, err := solcap.ConvertGetSlotHeightRequestFromProto(input)
	if err != nil {
		return nil, NewUserError(fmt.Errorf("invalid request: %w", err))
	}

	lggr := s.messageBuilder.RequestLggr(s.lggr, monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}).With("request", request)
	cReq := ctypes.NewAggregatableRequest(commonMon.RequestID(metadata.WorkflowExecutionID, metadata.ReferenceID), func(ctx context.Context) (*ctypes.AggregatableObservation, error) {
		rawResponse, err := s.SolanaService.GetSlotHeight(ctx, request)
		if err != nil {
			return nil, err
		}

		return &ctypes.AggregatableObservation{
			Method: ctypes.AggregationMethodFPlusOneHighest,
			Value: &valuespb.Decimal{
				Coefficient: valuespb.NewBigIntFromInt(new(big.Int).SetUint64(rawResponse.Height)),
				Exponent:    0,
			},
		}, nil
	})
	aggregatedHeight, err := chainconsensus.ReadDecimal(ctx, s.handler, cReq)
	if err != nil {
		return nil, getReadError(lggr, fmt.Errorf("failed to GetSlotHeight: %w", err))
	}

	return &capabilities.ResponseAndMetadata[*solcap.GetSlotHeightReply]{
		Response:         &solcap.GetSlotHeightReply{Height: aggregatedHeight.BigInt().Uint64()},
		ResponseMetadata: metering.GetResponseMetadata(metering.GetAccountInfo),
	}, nil
}

func (s *Solana) GetTransaction(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetTransactionRequest) (*capabilities.ResponseAndMetadata[*solcap.GetTransactionReply], caperrors.Error) {
	if !s.readsEnabled {
		return nil, caperrors.NewPublicSystemError(errors.New("reads are not available"), caperrors.Internal)
	}
	request, err := solcap.ConvertGetTransactionRequestFromProto(input)
	if err != nil {
		return nil, NewUserError(fmt.Errorf("invalid request: %w", err))
	}
	request.IsExternal = true
	lggr := s.messageBuilder.RequestLggr(s.lggr, monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}).With("request", request)
	cReq := ctypes.NewECHashableRequest(metadata.WorkflowExecutionID, metadata.ReferenceID, metering.GetResponseMetadata(metering.GetAccountInfo), func(ctx context.Context) (*solcap.GetTransactionReply, error) {
		rawResponse, err := s.SolanaService.GetTransaction(ctx, request)
		if err != nil {
			return nil, err
		}

		response, err := solcap.ConvertGetTransactionReplyToProto(rawResponse)
		if err != nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to convert response to proto: %w", err), caperrors.Internal)
		}

		if err = s.checkReadPayloadSize(ctx, response); err != nil {
			return nil, NewUserError(err)
		}
		return response, nil
	})
	responseAndMetadata, err := chainconsensus.ReadHashableRequestReport[*solcap.GetTransactionReply](ctx, s.handler, cReq)
	if err != nil {
		return nil, getReadError(lggr, fmt.Errorf("failed to GetTransaction: %w", err))
	}

	return responseAndMetadata, nil
}

func (s *Solana) GetProgramAccounts(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetProgramAccountsRequest) (*capabilities.ResponseAndMetadata[*solcap.GetProgramAccountsReply], caperrors.Error) {
	if !s.readsEnabled {
		return nil, caperrors.NewPublicSystemError(errors.New("reads are not available"), caperrors.Internal)
	}
	request, err := solcap.ConvertGetProgramAccountsRequestFromProto(input)
	if err != nil {
		return nil, NewUserError(fmt.Errorf("invalid request: %w", err))
	}
	request.IsExternal = true
	lggr := s.messageBuilder.RequestLggr(s.lggr, monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}).With("request", request)
	cReq := ctypes.NewVolatileRequest(metadata.WorkflowExecutionID, metadata.ReferenceID, metering.GetResponseMetadata(metering.GetAccountInfo), func(ctx context.Context) (*solcap.GetProgramAccountsReply, uint64, error) {
		rawResponse, err := s.SolanaService.GetProgramAccounts(ctx, *request)
		if err != nil {
			return nil, 0, err
		}

		// getProgramAccounts does not guarantee ordering across RPC nodes.
		// Sort by pubkey so all nodes produce an identical hash.
		slices.SortFunc(rawResponse.Value, func(a, b *soltypes.KeyedAccount) int {
			return bytes.Compare(a.Pubkey[:], b.Pubkey[:])
		})

		response, err := solcap.ConvertGetProgramAccountsReplyToProto(rawResponse)
		if err != nil {
			return nil, 0, caperrors.NewPublicSystemError(fmt.Errorf("failed to convert response to proto: %w", err), caperrors.Internal)
		}

		if err = s.checkReadPayloadSize(ctx, response); err != nil {
			return nil, 0, NewUserError(err)
		}
		return response, 0, nil
	}, lggr)
	responseAndMetadata, err := chainconsensus.ReadHashableRequestReport[*solcap.GetProgramAccountsReply](ctx, s.handler, cReq)
	if err != nil {
		return nil, getReadError(lggr, fmt.Errorf("failed to GetProgramAccounts: %w", err))
	}

	return responseAndMetadata, nil
}

func (s *Solana) MonitoringContext() capmon.MonitoringContext {
	return capmon.MonitoringContext{
		Logger:            s.lggr,
		MetricsAttributes: s.messageBuilder.CapabilityMetricsAttributes,
	}
}

func (s *Solana) initLimiters(limitsFactory limits.Factory) (err error) {
	// PLEX-1920 this is initial values taken from chainlink-solana/docs/forwarder. Can be tuned later
	s.reportSizeLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainWrite.Solana.ReportSizeLimit)
	if err != nil {
		return
	}

	s.txComputeLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainWrite.Solana.GasLimit)
	if err != nil {
		return
	}

	s.readPayloadSizeLimiter, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainRead.Solana.PayloadSizeLimit)
	if err != nil {
		return
	}

	s.batchItemLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainRead.Solana.BatchItemLimit)
	return
}

func (s *Solana) Close() error {
	return services.CloseAll(s.reportSizeLimit, s.txComputeLimit, s.readPayloadSizeLimiter, s.batchItemLimit)
}

func (s *Solana) checkReadPayloadSize(ctx context.Context, msg proto.Message) error {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	if err != nil {
		return err
	}
	return s.readPayloadSizeLimiter.Check(ctx, commoncfg.SizeOf(b))
}

func (s *Solana) validateBatchItemCount(ctx context.Context, count int) error {
	return s.batchItemLimit.Check(ctx, count)
}

func getReadError(lggr logger.SugaredLogger, err error) caperrors.Error {
	if err == nil {
		return nil
	}

	isUserErr := isUserError(err)
	capErr := GetError(err, isUserErr)

	// TODO: logging of init, success and error should be move to a higher level
	lggr = lggr.With("error", err)
	const msg = "Read operation failed"
	if isUserErr {
		lggr.Debug(msg)
	} else {
		lggr.Error(msg)
	}

	return capErr
}

func isUserError(err error) bool {
	return !errors.Is(err, context.DeadlineExceeded) && !isNodeInfraError(err)
}

func isNodeInfraError(err error) bool {
	return errors.Is(err, multinode.ErrNodeError) ||
		strings.Contains(err.Error(), multinode.ErrNodeError.Error())
}

var GetError = capcommon.GetError
var NewUserError = capcommon.NewUserError
