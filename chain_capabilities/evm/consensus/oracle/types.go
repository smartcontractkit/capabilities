package oracle

import (
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
)

type BlocksProvider interface {
	GetLatest() (int64, error)
	GetSafe() (int64, error)
	GetFinalized() (int64, error)
}

type RequestsHandler interface {
	GetRequestIDs(batchSize int) ([]string, error)
	GetRequest(id string) (types.Request, bool)
	CompleteRequest(id string, report *types.RequestReport) error
}
