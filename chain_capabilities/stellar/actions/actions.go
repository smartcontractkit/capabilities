package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-framework/multinode"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/metering"
)

// Stellar implements the CRE capability actions for the Stellar chain
type Stellar struct {
	types.StellarService
	handler        chainconsensus.RequestHandler
	lggr           logger.SugaredLogger
	messageBuilder *commonmon.MessageBuilder
	chainSelector  uint64
}

// NewStellar builds the Stellar capability actions.
func NewStellar(
	service types.StellarService,
	lggr logger.Logger,
	chainSelector uint64,
	handler chainconsensus.RequestHandler,
	messageBuilder *commonmon.MessageBuilder,
) (*Stellar, error) {
	return &Stellar{
		StellarService: service,
		handler:        handler,
		lggr:           logger.Sugared(lggr),
		messageBuilder: messageBuilder,
		chainSelector:  chainSelector,
	}, nil
}

// ReadContract performs a consensus read of a read-only Soroban contract call.
func (s *Stellar) ReadContract(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *stellarcap.ReadContractRequest,
) (*capabilities.ResponseAndMetadata[*stellarcap.ReadContractResponse], caperrors.Error) {
	request, err := stellarcap.ConvertReadContractRequestFromProto(input)
	if err != nil {
		return nil, NewUserError(fmt.Errorf("invalid request: %w", err), caperrors.InvalidArgument)
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

	cReq := ctypes.NewVolatileRequest(
		metadata.WorkflowExecutionID,
		metadata.ReferenceID,
		metering.GetResponseMetadata(metering.ReadContract),
		func(ctx context.Context) (*stellarcap.ReadContractResponse, uint64, error) {
			response, err := s.StellarService.ReadContract(ctx, request)
			if err != nil {
				return nil, 0, err
			}

			return &stellarcap.ReadContractResponse{
				Result:         response.Result,
				LedgerSequence: response.LedgerSequence,
				Error:          response.Error,
			}, uint64(response.LedgerSequence), nil
		},
		lggr,
	)

	responseAndMetadata, err := chainconsensus.ReadHashableRequestReport[*stellarcap.ReadContractResponse](ctx, s.handler, cReq)
	if err != nil {
		return nil, getReadError(lggr, fmt.Errorf("failed to ReadContract: %w", err))
	}

	resp := responseAndMetadata.Response
	lggr.Debugw("Successfully handled ReadContract",
		"ledgerSequence", resp.GetLedgerSequence(),
		"resultByteLength", len(resp.GetResult()),
	)
	return responseAndMetadata, nil
}

func (s *Stellar) GetLatestLedger(
	_ context.Context,
	_ capabilities.RequestMetadata,
	_ *stellarcap.GetLatestLedgerRequest,
) (*capabilities.ResponseAndMetadata[*stellarcap.GetLatestLedgerResponse], caperrors.Error) {
	return nil, caperrors.NewPublicSystemError(errors.New("unimplemented"), caperrors.Unknown)
}

func (s *Stellar) WriteReport(
	_ context.Context,
	_ capabilities.RequestMetadata,
	_ *stellarcap.WriteReportRequest,
) (*capabilities.ResponseAndMetadata[*stellarcap.WriteReportReply], caperrors.Error) {
	return nil, caperrors.NewPublicSystemError(errors.New("unimplemented"), caperrors.Unknown)
}

func getReadError(lggr logger.SugaredLogger, err error) caperrors.Error {
	if err == nil {
		return nil
	}

	isUserErr := isUserError(err)
	capErr := GetError(err, isUserErr)

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
	return !errors.Is(err, context.DeadlineExceeded) && !isStellarNodeInfraError(err)
}

// isStellarNodeInfraError reports whether err is a node-availability failure. It checks both
// error identity and the message substring because errors reach this function through LOOP gRPC ,
// which preserve only the gRPC status code and message — Go error identity (errors.Is) does not survive that round trip.
func isStellarNodeInfraError(err error) bool {
	return errors.Is(err, multinode.ErrNodeError) || strings.Contains(err.Error(), multinode.ErrNodeError.Error())
}

var GetError = capcommon.GetError
var NewUserError = caperrors.NewPublicUserError
