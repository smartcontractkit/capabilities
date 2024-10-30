package main

import (
	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/capabilities/streams/server"
)

const (
	serviceName = "StreamsCapabilities"
)

func main() {
	loopserver.Create("StreamsCapabilities", server.New)
}
