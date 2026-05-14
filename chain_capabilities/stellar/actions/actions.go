package actions

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
)

// Stellar implements the CRE capability actions for the Stellar chain.
type Stellar struct {
	types.StellarService
}

func (s *Stellar) Close() error {
	return fmt.Errorf("unimplemented")
}

func (s *Stellar) WriteReport(_ context.Context, _ capabilities.RequestMetadata, _ *stellarcap.WriteReportRequest) (*capabilities.ResponseAndMetadata[*stellarcap.WriteReportReply], caperrors.Error) {
	return nil, caperrors.NewPublicSystemError(fmt.Errorf("unimplemented"), caperrors.Unknown)
}

func (s *Stellar) GetLatestLedger(_ context.Context, _ capabilities.RequestMetadata, _ *stellarcap.GetLatestLedgerRequest) (*capabilities.ResponseAndMetadata[*stellarcap.GetLatestLedgerResponse], caperrors.Error) {
	return nil, caperrors.NewPublicSystemError(fmt.Errorf("unimplemented"), caperrors.Unknown)
}

func (s *Stellar) ReadContract(_ context.Context, _ capabilities.RequestMetadata, protoReq *stellarcap.ReadContractRequest) (*capabilities.ResponseAndMetadata[*stellarcap.ReadContractResponse], caperrors.Error) {
	_, _ = stellarcap.ConvertReadContractRequestFromProto(protoReq)
	return nil, caperrors.NewPublicSystemError(fmt.Errorf("unimplemented"), caperrors.Unknown)
}
