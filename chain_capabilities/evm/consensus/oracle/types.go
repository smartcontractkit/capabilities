package oracle

import (
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"

	"github.com/smartcontractkit/chain_capabilities/evm/consensus/types"
)

type BlocksProvider interface {
	GetLatest() (int64, error)
	GetSafe() (int64, error)
	GetFinalized() (int64, error)
}

type RequestsStore interface {
	GetRequestIDs(batchSize int) ([]string, error)
	GetRequest(id string) (types.Request, bool)
	GetObservation(id string) ([]byte, bool)
	CompleteRequest(id string, report *evmservice.RequestReport) error
	MarkAttempted(id string)
}
