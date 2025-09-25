package main

import (
	"github.com/smartcontractkit/capabilities/capabilitywatcher/server"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

func main() {
	loopserver.Serve("CapabilityWatcher", server.New)
}
