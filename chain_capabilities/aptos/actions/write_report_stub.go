package actions

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
)

// WriteReport remains present to satisfy the generated Aptos capability server interface.
// This branch intentionally upstreams read-only Aptos capability behavior.
func (s *Aptos) WriteReport(
	_ context.Context,
	_ capabilities.RequestMetadata,
	_ *aptoscap.WriteReportRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.WriteReportReply], caperrors.Error) {
	return nil, NewUserError(fmt.Errorf("WriteReport is not supported in read-only Aptos capability mode"))
}
