package trigger

import (
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
)

type logPollCursor struct {
	blockNumber int64
	logIndex    int64
	txHashHex   string
	hasValue    bool
}

func (c logPollCursor) String() string {
	if !c.hasValue {
		return ""
	}
	// Parser only uses block and log index for keyset pagination. Keep the tx hash segment
	// so the cursor matches the expected block-logindex-txhash structure.
	return fmt.Sprintf("%d-%d-%s", c.blockNumber, c.logIndex, c.txHashHex)
}

func (c *logPollCursor) Commit(log *solana.Log) {
	if log == nil {
		return
	}
	c.blockNumber = log.BlockNumber
	c.logIndex = log.LogIndex
	c.txHashHex = fmt.Sprintf("%x", log.TxHash)
	c.hasValue = true
}
