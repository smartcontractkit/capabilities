package main

import (
	"github.com/smartcontractkit/capabilities/capabilitywatcher/server"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

func main() {
	loopserver.Serve("HealthCheck", server.New)
}
