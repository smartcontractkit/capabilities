package main

import (
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/consensus/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	"github.com/smartcontractkit/capabilities/consensus/action"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

func main() {
	loopserver.Serve("ConsensusCapability", func(lggr logger.Logger) loop.StandardCapabilities {
		return server.NewConsensusServer(action.NewConsensusCapability(lggr, clockwork.NewRealClock(), 1*time.Minute))
	})
}
