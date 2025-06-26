package actions

// rpcBlockNumber - duplicates rpc.BlockNumber from of github.com/ethereum/go-ethereum/rpc to avoid additional dependency
type rpcBlockNumber int64

const (
	safeBlockNumber      = rpcBlockNumber(-4)
	finalizedBlockNumber = rpcBlockNumber(-3)
	latestBlockNumber    = rpcBlockNumber(-2)
)
