package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/metering"
)

// Stellar implements the CRE capability actions for the Stellar chain
type Stellar struct {
	types.StellarService
	handler       chainconsensus.RequestHandler
	lggr          logger.SugaredLogger
	chainSelector uint64
}

// NewStellar builds the Stellar capability actions.
func NewStellar(
	service types.StellarService,
	lggr logger.Logger,
	chainSelector uint64,
	handler chainconsensus.RequestHandler,
) (*Stellar, error) {
	return &Stellar{
		StellarService: service,
		handler:        handler,
		lggr:           logger.Sugared(lggr),
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

	lggr := s.lggr.With("workflowExecutionID", metadata.WorkflowExecutionID, "referenceID", metadata.ReferenceID)
	lggr.Info("Received ReadContract request")

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

	lggr.Debugw("Successfully handled ReadContract")
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

// isUserError classifies an error as user-facing. Context deadline/cancellation are treated as
// system errors (transient infra), everything else is surfaced to the user.
func isUserError(err error) bool {
	return !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)
}

var GetError = capcommon.GetError
var NewUserError = caperrors.NewPublicUserError
