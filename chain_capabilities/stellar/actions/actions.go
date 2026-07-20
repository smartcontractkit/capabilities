package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-framework/multinode"
	"github.com/stellar/go-stellar-sdk/xdr"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/monitoring"
)

// Stellar implements the CRE capability actions for the Stellar chain.
type Stellar struct {
	types.StellarService
	handler                  chainconsensus.RequestHandler
	lggr                     logger.SugaredLogger
	messageBuilder           *monitoring.MessageBuilder
	beholderProcessor        beholder.ProtoProcessor
	chainSelector            uint64
	forwarderClient          CREForwarderClient
	forwarderLookbackLedgers int64
	reportSizeLimit          limits.BoundLimiter[commoncfg.Size]
	transmissionScheduler    ts.TransmissionScheduler
}

func NewStellar(
	service types.StellarService,
	forwarderAddress string,
	forwarderLookbackLedgers int64,
	lggr logger.Logger,
	limitsFactory limits.Factory,
	transmissionScheduler ts.TransmissionScheduler,
	chainSelector uint64,
	handler chainconsensus.RequestHandler,
	messageBuilder *monitoring.MessageBuilder,
	beholderProcessor beholder.ProtoProcessor,
) (*Stellar, error) {
	if service == nil {
		return nil, fmt.Errorf("stellar service is required")
	}

	st := &Stellar{
		StellarService:           service,
		handler:                  handler,
		lggr:                     logger.Sugared(lggr),
		messageBuilder:           messageBuilder,
		beholderProcessor:        beholderProcessor,
		chainSelector:            chainSelector,
		forwarderClient:          newForwarderClient(service, lggr, forwarderAddress, forwarderLookbackLedgers),
		forwarderLookbackLedgers: forwarderLookbackLedgers,
		transmissionScheduler:    transmissionScheduler,
	}
	return st, st.initLimiters(limitsFactory)
}

func (s *Stellar) initLimiters(limitsFactory limits.Factory) (err error) {
	s.reportSizeLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainWrite.ReportSizeLimit)
	return err
}

func (s *Stellar) Close() error {
	return services.CloseAll(s.reportSizeLimit)
}

// GetLatestLedger performs a consensus read of the current ledger. Each node
// observes its latest ledger and the DON aggregates via the OCR consensus handler
// (same path as ReadContract), keyed by the observed ledger sequence.
func (s *Stellar) GetLatestLedger(ctx context.Context, metadata capabilities.RequestMetadata, _ *stellarcap.GetLatestLedgerRequest) (*capabilities.ResponseAndMetadata[*stellarcap.GetLatestLedgerResponse], caperrors.Error) {
	s.lggr.Debug("Received GetLatestLedger request")

	observe := func(ctx context.Context, height *ctypes.ChainHeight) (*stellarcap.GetLatestLedgerResponse, error) {
		if height == nil || height.Latest <= 0 {
			return nil, fmt.Errorf("no agreed chain height available for GetLatestLedger consensus")
		}
		// stellar ledger sequences are uint32 on-chain
		if height.Latest > math.MaxUint32 {
			return nil, fmt.Errorf("agreed ledger sequence %d exceeds uint32", height.Latest)
		}
		return s.fetchLatestLedgerMetadata(ctx, uint32(height.Latest))
	}

	request := ctypes.NewLockableToBlockHashableRequest(
		metadata.WorkflowExecutionID,
		metadata.ReferenceID,
		metering.GetResponseMetadata(metering.GetLatestLedger),
		observe,
	)

	responseAndMetadata, err := chainconsensus.ReadHashableRequestReport(ctx, s.handler, request)
	if err != nil {
		s.lggr.Errorw("Failed to GetLatestLedger", "error", err)
		return nil, capcommon.GetError(fmt.Errorf("failed to GetLatestLedger: %w", err), false)
	}
	return responseAndMetadata, nil
}

// ReadContract performs a consensus read of a read-only Soroban contract call.
func (s *Stellar) ReadContract(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *stellarcap.ReadContractRequest,
) (*capabilities.ResponseAndMetadata[*stellarcap.ReadContractResponse], caperrors.Error) {
	request, err := convertReadContractRequestFromProto(input)
	if err != nil {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("invalid request: %w", err), caperrors.InvalidArgument)
	}

	tc := commonmon.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}
	lggr := s.messageBuilder.RequestLggr(s.lggr, tc).With(
		"chainSelector", s.chainSelector,
		"contractID", request.ContractID,
		"function", request.Function,
		"sourceAccount", request.SourceAccount,
		"argsCount", len(request.Args),
	)
	lggr.Debug("Received ReadContract request")

	monitoring.EmitInitiated(ctx, s.lggr, s.beholderProcessor, s.messageBuilder.BuildReadContractInitiated(tc, request))

	cReq := ctypes.NewVolatileRequest(
		metadata.WorkflowExecutionID,
		metadata.ReferenceID,
		metering.GetResponseMetadata(metering.ReadContract),
		func(ctx context.Context) (*stellarcap.ReadContractResponse, uint64, error) {
			response, err := s.SimulateTransaction(ctx, request)
			if err != nil {
				return nil, 0, err
			}

			return &stellarcap.ReadContractResponse{
				Result:         response.ReturnValueXDR,
				LedgerSequence: response.LedgerSequence,
				Error:          response.Error,
			}, uint64(response.LedgerSequence), nil
		},
		lggr,
	)

	responseAndMetadata, err := chainconsensus.ReadHashableRequestReport[*stellarcap.ReadContractResponse](ctx, s.handler, cReq)
	if err != nil {
		capErr := capcommon.GetError(err, isUserError(fmt.Errorf("failed to ReadContract: %w", err)))
		monitoring.LogAndEmitError(ctx, s.lggr, s.beholderProcessor,
			s.messageBuilder.BuildReadContractError(tc, request, "Failed to ReadContract", capErr))
		return nil, capErr
	}

	resp := responseAndMetadata.Response
	monitoring.LogAndEmitSuccess(ctx, "Successfully handled ReadContract", s.lggr, s.beholderProcessor,
		s.messageBuilder.BuildReadContractSuccess(tc, request, uint64(len(resp.GetResult())), resp.GetLedgerSequence()))
	return responseAndMetadata, nil
}

func convertReadContractRequestFromProto(p *stellarcap.ReadContractRequest) (stellartypes.SimulateTransactionRequest, error) {
	if p == nil {
		return stellartypes.SimulateTransactionRequest{}, fmt.Errorf("readContractRequest is nil")
	}
	if p.GetContractId() == "" {
		return stellartypes.SimulateTransactionRequest{}, fmt.Errorf("contractID is required")
	}
	if p.GetFunction() == "" {
		return stellartypes.SimulateTransactionRequest{}, fmt.Errorf("function is required")
	}

	pArgs := p.GetArgs()
	args := make([]stellartypes.ScVal, len(pArgs))
	for i, psv := range pArgs {
		sv, err := stellarcap.ProtoToScVal(psv)
		if err != nil {
			return stellartypes.SimulateTransactionRequest{}, fmt.Errorf("args[%d]: %w", i, err)
		}
		args[i] = sv
	}
	return stellartypes.SimulateTransactionRequest{
		ContractID:    p.GetContractId(),
		Function:      p.GetFunction(),
		Args:          args,
		SourceAccount: p.GetSourceAccount(),
	}, nil
}

func (s *Stellar) isUserErrorWriteReport(err error) bool {
	return strings.HasPrefix(err.Error(), capcommon.UserError)
}

func (s *Stellar) Info() (capabilities.CapabilityInfo, error) {
	return capabilities.CapabilityInfo{}, nil
}

func (s *Stellar) fetchLatestLedgerMetadata(ctx context.Context, latestLedger uint32) (*stellarcap.GetLatestLedgerResponse, error) {
	resp, err := s.GetLedgers(ctx, stellartypes.GetLedgersRequest{
		StartLedger: latestLedger,
		Pagination:  &stellartypes.LedgerPaginationOptions{Limit: 1},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to GetLedgers: %w", err)
	}

	if len(resp.Ledgers) == 0 {
		return nil, fmt.Errorf("no ledger returned for sequence %d", latestLedger)
	}
	ledger := resp.Ledgers[0]
	if ledger.Sequence != latestLedger {
		return nil, fmt.Errorf("rpc returned ledger %d, expected %d", ledger.Sequence, latestLedger)
	}

	out := &stellarcap.GetLatestLedgerResponse{
		Sequence:        latestLedger,
		LedgerCloseTime: ledger.LedgerCloseTime,
	}

	hash, err := hex.DecodeString(ledger.Hash)
	if err != nil {
		return nil, fmt.Errorf("decode ledger hash %q: %w", ledger.Hash, err)
	}
	out.Hash = hash

	// extract encoded data for cleaner sdk response (ok when fetching one block)
	if ledger.LedgerHeaderXDR != "" {
		var hist xdr.LedgerHeaderHistoryEntry
		if err = xdr.SafeUnmarshalBase64(ledger.LedgerHeaderXDR, &hist); err != nil {
			return nil, fmt.Errorf("failed  to decode ledger header xdr: %w", err)
		}
		headerBin, err := hist.Header.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("failed to marshal ledger header: %w", err)
		}
		out.LedgerHeaderXdr = headerBin
		out.ProtocolVersion = uint32(hist.Header.LedgerVersion)
	}

	if ledger.LedgerMetadataXDR != "" {
		var meta xdr.LedgerCloseMeta
		if err = xdr.SafeUnmarshalBase64(ledger.LedgerMetadataXDR, &meta); err != nil {
			return nil, fmt.Errorf("failed to decode ledger metadata xdr: %w", err)
		}
		if v2, ok := meta.GetV2(); ok {
			metaBin, err := v2.MarshalBinary()
			if err != nil {
				return nil, fmt.Errorf("failed to marshal ledger close meta v2: %w", err)
			}
			out.LedgerMetadataXdr = metaBin
		}
	}

	return out, nil
}

func isUserError(err error) bool {
	return !errors.Is(err, context.DeadlineExceeded) && !isStellarNodeInfraError(err)
}

// isStellarNodeInfraError reports whether err is a node-availability failure. It checks both
// error identity and the message substring because errors reach this function through LOOP gRPC,
// which preserves only the gRPC status code and message.
func isStellarNodeInfraError(err error) bool {
	return errors.Is(err, multinode.ErrNodeError) || strings.Contains(err.Error(), multinode.ErrNodeError.Error())
}
