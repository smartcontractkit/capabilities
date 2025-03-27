package main

import (
	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/capabilities/mock/server"
)

func main() {
	loopserver.Serve("MockCapability", server.New)
}
