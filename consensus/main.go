package main

import (
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/consensus/server"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	"github.com/smartcontractkit/capabilities/consensus/action"
	"github.com/smartcontractkit/capabilities/consensus/metrics"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

func main() {
	loopserver.ServeNewWithOtelViews("ConsensusCapability", func(s *loop.Server) loop.StandardCapabilities {
		capability, err := action.NewConsensusCapability(s.Logger, clockwork.NewRealClock(), 1*time.Minute, s.LimitsFactory)
		if err != nil {
			s.Logger.Fatalw("Failed to create ConsensusCapability", "error", err)
		}
		return server.NewConsensusServer(capability)
	}, metrics.MetricViews())
}
