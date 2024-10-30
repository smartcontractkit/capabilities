package main

import (
	"github.com/smartcontractkit/capabilities/libs/cll/capabilities/execution"

	"github.com/smartcontractkit/capabilities/streams/server"
)

const (
	serviceName = "StreamsCapabilities"
)

func main() {
	execution.RunCapability(serviceName, server.New)
}
