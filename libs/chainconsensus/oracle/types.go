package oracle

import (
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

type BlocksProvider interface {
	GetLatest() int64
	GetSafe() int64
	GetFinalized() int64
}

type RequestsHandler interface {
	GetRequestIDs(batchSize int) ([]string, error)
	GetRequest(id string) (types.Request, bool)
	CompleteRequest(id string, report *types.RequestReport) error
}
