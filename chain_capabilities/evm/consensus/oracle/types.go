package oracle

import "github.com/smartcontractkit/chain_capabilities/evm/consensus/types"

type BlocksProvider interface {
	GetLatest() (int64, error)
	GetSafe() (int64, error)
	GetFinalized() (int64, error)
}

type RequestsStore interface {
	GetRequestIDs(batchSize int) ([]string, error)
	GetRequest(id string) (types.Request, bool)
	GetObservation(id string) ([]byte, bool)
	CompleteRequest(id string, result []byte)
	Update(request types.Request)
	MarkAttempted(id string)
}
